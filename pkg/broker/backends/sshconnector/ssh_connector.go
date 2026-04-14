// Copyright 2025 Velda Inc
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package sshconnector

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"google.golang.org/protobuf/encoding/protojson"
	pb "google.golang.org/protobuf/proto"
	"gopkg.in/yaml.v3"

	"velda.io/velda"
	agentpb "velda.io/velda/pkg/proto/agent"
	configpb "velda.io/velda/pkg/proto/config"
)

const (
	defaultSSHPort         = 22
	defaultConnectTimeout  = 20 * time.Second
	defaultReadyTimeout    = 24 * time.Hour
	defaultReadyPoll       = 5 * time.Second
	defaultRemoteCertPath  = "/etc/velda/mtls-agent-ca.pem"
	defaultAgentConfigPath = "/etc/velda/agent.yaml"
	defaultRemoteOutputDir = "/var/log/velda"
	defaultLocalOutputDir  = "/tmp/velda-ssh-connector"
)

// Config contains shared options for the connector. Runtime call sites only pass IP and hostname.
type Config struct {
	SSHPort           int
	SSHPrivateKeyPEM  []byte
	SSHPrivateKeyPath string
	ConnectTimeout    time.Duration
	ReadyTimeout      time.Duration
	ReadyPollInterval time.Duration

	LocalMTLSCertPath  string
	RemoteMTLSCertPath string

	// AgentConfig is the preferred way to supply the remote agent configuration.
	AgentConfig *agentpb.AgentConfig
	// Deprecated: set AgentConfig instead.
	AgentConfigContent    string
	RemoteAgentConfigPath string
	AgentVersionOverride  string

	TailscaleConfig *configpb.TailscaleConfig

	// Local output directory on the broker host for per-run bootstrap logs.
	LocalOutputLogDir string
}

type Defaults struct {
	SSHUser               string
	SSHPort               int
	SSHPrivateKeyPath     string
	ConnectTimeout        time.Duration
	ReadyTimeout          time.Duration
	ReadyPollInterval     time.Duration
	LocalMTLSCertPath     string
	RemoteMTLSCertPath    string
	RemoteAgentConfigPath string
	LocalOutputLogDir     string
	TailscaleConfig       *configpb.TailscaleConfig
}

var (
	connectorDefaults = Defaults{
		SSHUser:               "ubuntu",
		SSHPort:               defaultSSHPort,
		ConnectTimeout:        defaultConnectTimeout,
		ReadyTimeout:          defaultReadyTimeout,
		ReadyPollInterval:     defaultReadyPoll,
		RemoteMTLSCertPath:    defaultRemoteCertPath,
		RemoteAgentConfigPath: defaultAgentConfigPath,
		LocalOutputLogDir:     defaultLocalOutputDir,
	}
	hostnameSanitizer = regexp.MustCompile(`[^a-zA-Z0-9_.-]+`)
)

func ConfigureDefaultsFromProto(cfg *configpb.Config) {
	if cfg == nil {
		return
	}
	sc := cfg.GetSshConnector()
	if sc == nil {
		return
	}

	if sc.GetSshUser() != "" {
		connectorDefaults.SSHUser = sc.GetSshUser()
	}
	if sc.GetSshPort() > 0 {
		connectorDefaults.SSHPort = int(sc.GetSshPort())
	}
	if sc.GetSshPrivateKeyPath() != "" {
		connectorDefaults.SSHPrivateKeyPath = sc.GetSshPrivateKeyPath()
	}
	if sc.GetConnectTimeout() != nil && sc.GetConnectTimeout().AsDuration() > 0 {
		connectorDefaults.ConnectTimeout = sc.GetConnectTimeout().AsDuration()
	}
	if sc.GetReadyTimeout() != nil && sc.GetReadyTimeout().AsDuration() > 0 {
		connectorDefaults.ReadyTimeout = sc.GetReadyTimeout().AsDuration()
	}
	if sc.GetReadyPollInterval() != nil && sc.GetReadyPollInterval().AsDuration() > 0 {
		connectorDefaults.ReadyPollInterval = sc.GetReadyPollInterval().AsDuration()
	}
	if sc.GetLocalMtlsCertPath() != "" {
		connectorDefaults.LocalMTLSCertPath = sc.GetLocalMtlsCertPath()
	}
	if sc.GetRemoteMtlsCertPath() != "" {
		connectorDefaults.RemoteMTLSCertPath = sc.GetRemoteMtlsCertPath()
	}
	if sc.GetRemoteAgentConfigPath() != "" {
		connectorDefaults.RemoteAgentConfigPath = sc.GetRemoteAgentConfigPath()
	}
	if sc.GetLocalOutputLogDir() != "" {
		connectorDefaults.LocalOutputLogDir = sc.GetLocalOutputLogDir()
	}
	if sc.GetTailscaleConfig() != nil {
		connectorDefaults.TailscaleConfig = pb.Clone(sc.GetTailscaleConfig()).(*configpb.TailscaleConfig)
	}
}

// NewDefault creates a Connector using the pre-populated AgentConfig proto.
func NewDefault(agentConfig *agentpb.AgentConfig, agentVersionOverride string) (*Connector, Defaults, error) {
	d := connectorDefaults

	c, err := New(Config{
		SSHPort:               d.SSHPort,
		SSHPrivateKeyPath:     d.SSHPrivateKeyPath,
		ConnectTimeout:        d.ConnectTimeout,
		ReadyTimeout:          d.ReadyTimeout,
		ReadyPollInterval:     d.ReadyPollInterval,
		LocalMTLSCertPath:     d.LocalMTLSCertPath,
		RemoteMTLSCertPath:    d.RemoteMTLSCertPath,
		AgentConfig:           agentConfig,
		RemoteAgentConfigPath: d.RemoteAgentConfigPath,
		AgentVersionOverride:  agentVersionOverride,
		TailscaleConfig:       d.TailscaleConfig,
		LocalOutputLogDir:     d.LocalOutputLogDir,
	})
	if err != nil {
		return nil, d, err
	}
	return c, d, nil
}

type Connector struct {
	cfg    Config
	signer ssh.Signer
}

func New(cfg Config) (*Connector, error) {
	if cfg.SSHPort == 0 {
		cfg.SSHPort = defaultSSHPort
	}
	if cfg.ConnectTimeout == 0 {
		cfg.ConnectTimeout = defaultConnectTimeout
	}
	if cfg.ReadyTimeout == 0 {
		cfg.ReadyTimeout = defaultReadyTimeout
	}
	if cfg.ReadyPollInterval == 0 {
		cfg.ReadyPollInterval = defaultReadyPoll
	}
	if cfg.RemoteMTLSCertPath == "" {
		cfg.RemoteMTLSCertPath = defaultRemoteCertPath
	}
	if cfg.RemoteAgentConfigPath == "" {
		cfg.RemoteAgentConfigPath = defaultAgentConfigPath
	}
	if cfg.LocalOutputLogDir == "" {
		cfg.LocalOutputLogDir = defaultLocalOutputDir
	}

	keyBytes := cfg.SSHPrivateKeyPEM
	if len(keyBytes) == 0 {
		if cfg.SSHPrivateKeyPath == "" {
			return nil, fmt.Errorf("ssh private key is required")
		}
		var err error
		keyBytes, err = os.ReadFile(cfg.SSHPrivateKeyPath)
		if err != nil {
			return nil, fmt.Errorf("failed to read SSH private key: %w", err)
		}
	}

	signer, err := ssh.ParsePrivateKey(keyBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse SSH private key: %w", err)
	}

	return &Connector{cfg: cfg, signer: signer}, nil
}

func (c *Connector) waitForSSHClient(ctx context.Context, ip, sshUser string) (*ssh.Client, error) {
	if ip == "" {
		return nil, fmt.Errorf("ip is required")
	}
	if sshUser == "" {
		return nil, fmt.Errorf("ssh user is required")
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, c.cfg.ReadyTimeout)
	defer cancel()

	ticker := time.NewTicker(c.cfg.ReadyPollInterval)
	defer ticker.Stop()

	var lastErr error
	for {
		client, err := c.dial(ip, sshUser)
		if err == nil {
			return client, nil
		}
		lastErr = err
		select {
		case <-timeoutCtx.Done():
			if lastErr != nil {
				return nil, fmt.Errorf("ssh not ready for %s: %w", ip, lastErr)
			}
			return nil, fmt.Errorf("ssh not ready for %s: %w", ip, timeoutCtx.Err())
		case <-ticker.C:
			log.Printf("SSH not ready for %s: %v, retrying...", ip, lastErr)
		}
	}
}

// Bootstrap connects to a node and provisions required runtime dependencies.
// Only ip, hostname, and sshUser are runtime parameters; all other parameters are constructor-scoped.
func (c *Connector) Bootstrap(ctx context.Context, ip, hostname, sshUser string) error {
	if ip == "" {
		return fmt.Errorf("ip is required")
	}
	if hostname == "" {
		return fmt.Errorf("hostname is required")
	}
	if sshUser == "" {
		return fmt.Errorf("ssh user is required")
	}

	logID := uniqueLogID(hostname)
	localLogPath := filepath.Join(c.cfg.LocalOutputLogDir, logID+".log")
	log.Printf("Bootstrapping %s(%s) with ssh: %s\n", ip, hostname, localLogPath)

	client, err := c.waitForSSHClient(ctx, ip, sshUser)
	if err != nil {
		return err
	}
	defer client.Close()

	if c.cfg.LocalMTLSCertPath != "" {
		if err := c.uploadFileSCP(ctx, client, c.cfg.LocalMTLSCertPath, "/tmp/mtls-cert.pem"); err != nil {
			return fmt.Errorf("failed to upload mTLS cert via SCP: %w", err)
		}
	}

	script, err := c.buildBootstrapScript(hostname)
	if err != nil {
		return err
	}
	err = c.runRemoteScript(ctx, client, script, localLogPath, ip, hostname)
	if err != nil {
		return err
	}

	return nil
}

func (c *Connector) dial(ip, sshUser string) (*ssh.Client, error) {
	addr := fmt.Sprintf("%s:%d", ip, c.cfg.SSHPort)
	sshCfg := &ssh.ClientConfig{
		User:            sshUser,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(c.signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         c.cfg.ConnectTimeout,
	}
	client, err := ssh.Dial("tcp", addr, sshCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to SSH dial %s@%s: %w", sshUser, addr, err)
	}
	return client, nil
}

func (c *Connector) ensureRemoteDir(ctx context.Context, client *ssh.Client, remoteDir string) error {
	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("failed to create SSH session: %w", err)
	}
	defer session.Close()

	cmd := fmt.Sprintf("mkdir -p %s", shellSingleQuote(remoteDir))
	if err := session.Run(cmd); err != nil {
		return fmt.Errorf("failed to prepare remote directory %s: %w", remoteDir, err)
	}
	return nil
}

func (c *Connector) uploadFileSCP(ctx context.Context, client *ssh.Client, localPath, remotePath string) error {
	_ = ctx

	data, err := os.ReadFile(localPath)
	if err != nil {
		return fmt.Errorf("failed to read local file %s: %w", localPath, err)
	}
	fi, err := os.Stat(localPath)
	if err != nil {
		return fmt.Errorf("failed to stat local file %s: %w", localPath, err)
	}

	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("failed to create SSH session for SCP: %w", err)
	}
	defer session.Close()

	stdin, err := session.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to get stdin pipe for SCP: %w", err)
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to get stdout pipe for SCP: %w", err)
	}
	stderr, err := session.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to get stderr pipe for SCP: %w", err)
	}

	if err := session.Start("scp -t " + shellSingleQuote(remotePath)); err != nil {
		return fmt.Errorf("failed to start SCP remote command: %w", err)
	}

	if err := readScpAck(stdout, stderr); err != nil {
		return err
	}

	filename := path.Base(remotePath)
	mode := fi.Mode().Perm() & 0o777
	header := fmt.Sprintf("C%04o %d %s\n", mode, len(data), filename)
	if _, err := io.WriteString(stdin, header); err != nil {
		return fmt.Errorf("failed to write SCP header: %w", err)
	}
	if err := readScpAck(stdout, stderr); err != nil {
		return err
	}

	if _, err := stdin.Write(data); err != nil {
		return fmt.Errorf("failed to write SCP payload: %w", err)
	}
	if _, err := stdin.Write([]byte{0}); err != nil {
		return fmt.Errorf("failed to finalize SCP payload: %w", err)
	}
	if err := readScpAck(stdout, stderr); err != nil {
		return err
	}

	if err := stdin.Close(); err != nil {
		return fmt.Errorf("failed to close SCP stdin: %w", err)
	}
	if err := session.Wait(); err != nil {
		return fmt.Errorf("scp command failed: %w", err)
	}

	return nil
}

func readScpAck(stdout io.Reader, stderr io.Reader) error {
	buf := make([]byte, 1)
	if _, err := io.ReadFull(stdout, buf); err != nil {
		return fmt.Errorf("failed to read SCP ack: %w", err)
	}
	if buf[0] == 0 {
		return nil
	}

	errLine, _ := io.ReadAll(stderr)
	if len(errLine) == 0 {
		errLine, _ = io.ReadAll(stdout)
	}
	if len(errLine) == 0 {
		return fmt.Errorf("remote SCP error (code=%d)", buf[0])
	}
	return fmt.Errorf("remote SCP error (code=%d): %s", buf[0], strings.TrimSpace(string(errLine)))
}

func (c *Connector) runRemoteScript(ctx context.Context, client *ssh.Client, script, localLogPath, ip, hostname string) error {
	if err := os.MkdirAll(path.Dir(localLogPath), 0o755); err != nil {
		return fmt.Errorf("failed to create local log dir: %w", err)
	}

	f, err := os.OpenFile(localLogPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("failed to open local output log: %w", err)
	}
	defer f.Close()

	if _, err := fmt.Fprintf(
		f,
		"[%s] host=%s ip=%s\n--- script ---\n%s\n--- output ---\n",
		time.Now().Format(time.RFC3339),
		hostname,
		ip,
		script,
	); err != nil {
		return fmt.Errorf("failed to write local output log header: %w", err)
	}

	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("failed to create SSH session for bootstrap: %w", err)
	}
	defer session.Close()

	stdin, err := session.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to get bootstrap stdin: %w", err)
	}

	session.Stdout = f
	session.Stderr = f

	if err := session.Start("sudo bash -xs"); err != nil {
		return fmt.Errorf("failed to start remote bootstrap script: %w", err)
	}

	if _, err := io.WriteString(stdin, script); err != nil {
		return fmt.Errorf("failed to send bootstrap script: %w", err)
	}
	if err := stdin.Close(); err != nil {
		return fmt.Errorf("failed to close bootstrap stdin: %w", err)
	}

	waitCh := make(chan error, 1)
	go func() {
		waitCh <- session.Wait()
	}()

	select {
	case <-ctx.Done():
		_ = session.Close()
		_, _ = fmt.Fprintf(f, "\n--- status ---\nerror: bootstrap cancelled: %v\n", ctx.Err())
		return fmt.Errorf("bootstrap cancelled: %w", ctx.Err())
	case err := <-waitCh:
		if err != nil {
			_, _ = fmt.Fprintf(f, "\n--- status ---\nerror: %v\n", err)
			return fmt.Errorf("remote bootstrap failed: %w", err)
		}
	}

	if _, err := io.WriteString(f, "\n--- status ---\nsuccess\n"); err != nil {
		return fmt.Errorf("failed to finalize local output log: %w", err)
	}

	return nil
}

func (c *Connector) buildBootstrapScript(hostname string) (string, error) {
	version := c.cfg.AgentVersionOverride
	if version == "" {
		version = velda.Version
	}
	var agentConfigContent string
	var err error
	if c.cfg.AgentConfig != nil {
		agentConfigContent, err = serializeAgentConfig(c.cfg.AgentConfig, hostname)
	}
	if err != nil {
		return "", err
	}

	b := &strings.Builder{}
	fmt.Fprintf(b, "#!/bin/bash\n")
	fmt.Fprintf(b, "set -euo pipefail\n")
	fmt.Fprintf(b, "[[ -e /etc/velda/init.done ]] && echo 'Bootstrap already completed, exiting.' && exit 0\n")
	if c.cfg.LocalMTLSCertPath != "" {
		fmt.Fprintf(b, "mkdir -p %s\n", shellSingleQuote(path.Dir(c.cfg.RemoteMTLSCertPath)))
		fmt.Fprintf(b, "mv /tmp/mtls-cert.pem %s\n", shellSingleQuote(c.cfg.RemoteMTLSCertPath))
	}

	fmt.Fprintf(b, "if ! command -v curl >/dev/null 2>&1; then apt-get update && apt-get install -y curl; fi\n")
	fmt.Fprintf(b, "if ! dpkg -s nfs-common >/dev/null 2>&1; then apt-get update && apt-get install -y nfs-common; fi\n")

	if agentConfigContent != "" {
		fmt.Fprintf(b, "mkdir -p %s\n", shellSingleQuote(path.Dir(c.cfg.RemoteAgentConfigPath)))
		fmt.Fprintf(b, "cat << 'VELDA_CONFIG_EOF' > %s\n", shellSingleQuote(c.cfg.RemoteAgentConfigPath))
		fmt.Fprintf(b, "%s\n", agentConfigContent)
		fmt.Fprintf(b, "VELDA_CONFIG_EOF\n")
	}

	if c.cfg.LocalMTLSCertPath != "" {
		fmt.Fprintf(b, "chmod 0644 %s\n", shellSingleQuote(c.cfg.RemoteMTLSCertPath))
	}
	fmt.Fprintf(b, "nvidia-smi || true\n")
	fmt.Fprintf(b, "curl -fsSL https://releases.velda.io/nvidia-collect.sh -o /tmp/nvidia-collect.sh && bash /tmp/nvidia-collect.sh || true\n")

	if ts := c.cfg.TailscaleConfig; ts != nil && ts.GetServer() != "" && ts.GetPreAuthKey() != "" {
		fmt.Fprintf(b, "if ! command -v tailscale >/dev/null 2>&1; then curl -fsSL https://tailscale.com/install.sh | sh; fi\n")
		fmt.Fprintf(b, "cat << 'VELDA_TS_REAUTH_EOF' > /usr/local/bin/velda-tailscale-reauth.sh\n")
		fmt.Fprintf(b, "#!/bin/bash\n")
		fmt.Fprintf(b, "set -euo pipefail\n")
		fmt.Fprintf(b, "if tailscale status >/dev/null 2>&1; then\n")
		fmt.Fprintf(b, "  exit 0\n")
		fmt.Fprintf(b, "fi\n")
		fmt.Fprintf(b, "tailscale up --login-server=%s --authkey=%s --accept-routes\n", shellSingleQuote(ts.GetServer()), shellSingleQuote(ts.GetPreAuthKey()))
		fmt.Fprintf(b, "VELDA_TS_REAUTH_EOF\n")
		fmt.Fprintf(b, "chmod 0755 /usr/local/bin/velda-tailscale-reauth.sh\n")
		fmt.Fprintf(b, "cat << 'VELDA_TS_SYSTEMD_EOF' > /etc/systemd/system/velda-tailscale-reauth.service\n")
		fmt.Fprintf(b, "[Unit]\n")
		fmt.Fprintf(b, "Description=Velda Tailscale Re-authentication\n")
		fmt.Fprintf(b, "After=network-online.target tailscaled.service\n")
		fmt.Fprintf(b, "Wants=network-online.target tailscaled.service\n\n")
		fmt.Fprintf(b, "[Service]\n")
		fmt.Fprintf(b, "Type=oneshot\n")
		fmt.Fprintf(b, "ExecStart=/usr/local/bin/velda-tailscale-reauth.sh\n\n")
		fmt.Fprintf(b, "[Install]\n")
		fmt.Fprintf(b, "WantedBy=multi-user.target\n")
		fmt.Fprintf(b, "VELDA_TS_SYSTEMD_EOF\n")
		fmt.Fprintf(b, "systemctl daemon-reload\n")
		fmt.Fprintf(b, "systemctl enable velda-tailscale-reauth.service\n")
		fmt.Fprintf(b, "systemctl start velda-tailscale-reauth.service\n")
	}

	fmt.Fprintf(b, "if [ \"$(/bin/velda version 2>/dev/null || true)\" != %s ]; then\n", shellSingleQuote(version))
	fmt.Fprintf(b, "  curl -fsSL https://releases.velda.io/velda-%s-linux-amd64 -o /tmp/velda\n", version)
	fmt.Fprintf(b, "  chmod +x /tmp/velda\n")
	fmt.Fprintf(b, "  mv /tmp/velda /bin/velda\n")
	fmt.Fprintf(b, "fi\n")

	fmt.Fprintf(b, "if [ ! -e /usr/lib/systemd/system/velda-agent.service ]; then\n")
	fmt.Fprintf(b, "  curl -fsSL https://releases.velda.io/velda-agent.service -o /usr/lib/systemd/system/velda-agent.service\n")
	fmt.Fprintf(b, "fi\n")
	fmt.Fprintf(b, "systemctl daemon-reload\n")
	fmt.Fprintf(b, "systemctl enable velda-agent.service\n")
	fmt.Fprintf(b, "systemctl restart velda-agent.service\n")

	fmt.Fprintf(b, "touch /etc/velda/init.done\n")

	return b.String(), nil
}

// serializeAgentConfig clones the proto, injects agent_name from hostname, and serializes to YAML.
func serializeAgentConfig(agentConfig *agentpb.AgentConfig, hostname string) (string, error) {
	cloned := pb.Clone(agentConfig).(*agentpb.AgentConfig)
	if hostname != "" {
		if cloned.DaemonConfig == nil {
			cloned.DaemonConfig = &agentpb.DaemonConfig{}
		}
		cloned.DaemonConfig.AgentName = hostname
	}
	jsonBytes, err := protojson.Marshal(cloned)
	if err != nil {
		return "", fmt.Errorf("failed to marshal agent config proto: %w", err)
	}
	var obj interface{}
	if err := yaml.Unmarshal(jsonBytes, &obj); err != nil {
		return "", fmt.Errorf("failed to convert agent config to yaml: %w", err)
	}
	rendered, err := yaml.Marshal(obj)
	if err != nil {
		return "", fmt.Errorf("failed to marshal agent config yaml: %w", err)
	}
	return string(rendered), nil
}

func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func shellDoubleQuote(s string) string {
	replacer := strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "`", "\\`")
	return replacer.Replace(s)
}

func uniqueLogID(hostname string) string {
	h := hostnameSanitizer.ReplaceAllString(hostname, "_")
	if h == "" {
		h = "host"
	}
	buf := make([]byte, 4)
	_, _ = rand.Read(buf)
	return fmt.Sprintf("%s-%d-%s", h, time.Now().UnixNano(), hex.EncodeToString(buf))
}
