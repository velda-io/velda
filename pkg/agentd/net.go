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
package agentd

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"os"
	"runtime"
	"strings"
	"sync"

	"github.com/google/nftables"
	"github.com/google/nftables/expr"
	"github.com/vishvananda/netlink"
	"github.com/vishvananda/netns"
	"golang.org/x/sys/unix"

	agentpb "velda.io/velda/pkg/proto/agent"
)

const PortWithNamespace = 2222

type NetworkBinding struct {
	AgentPort    int
	NetNamespace int32
	Addr         net.IP
}

type NetworkDaemon struct {
	mu     sync.Mutex
	pool   []NetworkBinding
	hostNs bool
}

func GetNetworkDaemon(maxSize int, cfg *agentpb.DaemonConfig_Network) (*NetworkDaemon, error) {
	result := &NetworkDaemon{}
	err := result.init(maxSize, cfg)
	if err != nil {
		return nil, err
	}
	return result, nil
}

// Returns a file-handle to a network namespace.
func (nd *NetworkDaemon) Get() NetworkBinding {
	nd.mu.Lock()
	defer nd.mu.Unlock()
	if len(nd.pool) == 0 {
		panic("NetworkDaemon pool is empty")
	}
	fd := nd.pool[len(nd.pool)-1]
	nd.pool = nd.pool[:len(nd.pool)-1]
	return fd
}

func (nd *NetworkDaemon) Put(binding NetworkBinding) {
	nd.mu.Lock()
	defer nd.mu.Unlock()
	nd.pool = append(nd.pool, binding)
}

func (nd *NetworkDaemon) Close() error {
	if nd.hostNs {
		return nil
	}
	nd.mu.Lock()
	defer nd.mu.Unlock()
	for i := 0; i < len(nd.pool); i++ {
		fd := nd.pool[i].NetNamespace
		if fd > 0 {
			unix.Close(int(fd))
		}
	}
	return nil
}

func (nd *NetworkDaemon) init(maxSize int, cfg *agentpb.DaemonConfig_Network) error {
	if maxSize <= 0 {
		return nil
	}
	if cfg == nil {
		return nd.initHostNetwork(maxSize)
	}
	switch cfg.NetworkMode {
	case agentpb.DaemonConfig_Network_NETWORK_MODE_UNSPECIFIED:
	case agentpb.DaemonConfig_Network_NETWORK_MODE_HOST:
		return nd.initHostNetwork(maxSize)
	case agentpb.DaemonConfig_Network_NETWORK_MODE_BRIDGE:
		return nd.initBridgeNetwork(maxSize, cfg)
	}
	return nil
}

func (nd *NetworkDaemon) initHostNetwork(maxSize int) error {
	nd.hostNs = true
	for i := 1; i <= maxSize; i++ {
		nd.pool = append(nd.pool, NetworkBinding{
			AgentPort:    0,
			NetNamespace: -1, // 0 means host network
		})
	}
	return nil
}

func detectPrimaryIface() (string, error) {
	file, err := os.Open("/proc/net/route")
	if err != nil {
		return "", err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	// Skip header
	scanner.Scan()

	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 11 {
			continue
		}
		if fields[1] == "00000000" { // Default route
			return fields[0], nil
		}
	}

	return "", fmt.Errorf("default route not found")
}

func (nd *NetworkDaemon) initBridgeNetwork(maxSize int, cfg *agentpb.DaemonConfig_Network) error {
	externalIface, err := detectPrimaryIface()
	if err != nil {
		return fmt.Errorf("failed to detect primary interface: %w", err)
	}
	if maxSize > 254 {
		return fmt.Errorf("maxSize %d is too large, must be <= 254", maxSize)
	}
	bridgeName := "br0"
	// Default bridge CIDR
	bridgeCIDR := "172.31.200.1/24"
	switch ipCfg := cfg.AgentIp.(type) {
	case *agentpb.DaemonConfig_Network_PrivateCidrRange:
		bridgeCIDR = ipCfg.PrivateCidrRange
	case *agentpb.DaemonConfig_Network_DetectGcpAliasIpRanges:
		// TODO
		return fmt.Errorf("GCP alias IP ranges not supported yet")
	}

	// Assign IP to bridge
	log.Printf("Setup networking bridge %s up %s with CIDR: %s", bridgeName, externalIface, bridgeCIDR)
	bridgeIP, err := netlink.ParseAddr(bridgeCIDR)
	if err != nil {
		return fmt.Errorf("invalid bridge IP: %w", err)
	}
	nsGW := bridgeIP.IP
	// Check mask is large enough for maxSize
	maskSize, _ := bridgeIP.Mask.Size()
	if (1 << (32 - maskSize)) < maxSize+2 { // +2 for bridge IP and gateway
		return fmt.Errorf("bridge CIDR %s is not large enough for %d namespaces", bridgeCIDR, maxSize)
	}

	runtime.LockOSThread() // required for namespace switching
	defer runtime.UnlockOSThread()
	if err := setupBridge(bridgeName, bridgeIP); err != nil {
		return fmt.Errorf("failed to create bridge: %w", err)
	}

	currentNs, err := netns.Get()
	if err != nil {
		return fmt.Errorf("failed to get current netns: %w", err)
	}
	for i := 1; i <= maxSize; i++ {
		nsName := fmt.Sprintf("ns%d", i)
		// nsIP = nsGW + i
		nsIp := &net.IPNet{
			IP:   append(net.IP{}, nsGW...),
			Mask: bridgeIP.Mask,
		}
		nsIp.IP[len(nsIp.IP)-1] += byte(i) // Increment last byte for each namespace

		if nh, err := setupNamespaceViaBridge(currentNs, nsName, bridgeName, nsIp, nsGW); err != nil {
			return fmt.Errorf("failed to create namespace %s: %w", nsName, err)
		} else {
			nd.pool = append(nd.pool, NetworkBinding{AgentPort: PortWithNamespace + i, NetNamespace: int32(nh), Addr: nsIp.IP})
		}
	}

	if err := nd.setupNftablesNAT(externalIface); err != nil {
		return fmt.Errorf("NAT setup failed: %w", err)
	}

	return nil
}

func setupBridge(bridgeName string, bridgeIP *netlink.Addr) error {
	// Check if bridge exists
	_, err := netlink.LinkByName(bridgeName)
	if err == nil {
		// Already exists, skip
		return nil
	}

	la := netlink.NewLinkAttrs()
	la.Name = bridgeName
	br := &netlink.Bridge{LinkAttrs: la}

	if err := netlink.LinkAdd(br); err != nil {
		return fmt.Errorf("failed to add bridge: %w", err)
	}

	if err := netlink.LinkSetUp(br); err != nil {
		return fmt.Errorf("failed to set bridge up: %w", err)
	}

	if err := netlink.AddrAdd(br, bridgeIP); err != nil {
		return fmt.Errorf("failed to add addr to bridge: %w", err)
	}

	return nil
}

func setupNamespaceViaBridge(currentNs netns.NsHandle, nsName string, brName string, nsIP *net.IPNet, nsGateway net.IP) (nsHandle netns.NsHandle, err error) {

	vethHost := nsName + "-host"
	vethNs := nsName + "-ns"

	veth := &netlink.Veth{
		LinkAttrs: netlink.LinkAttrs{
			Name: vethHost,
		},
		PeerName: vethNs,
	}

	if err := netlink.LinkAdd(veth); err != nil {
		return netns.None(), fmt.Errorf("veth add failed: %w", err)
	}

	// Move peer to namespace
	peer, err := netlink.LinkByName(vethNs)
	if err != nil {
		return netns.None(), fmt.Errorf("failed to get peer link: %w", err)
	}

	// Attach host end to bridge
	hostLink, err := netlink.LinkByName(vethHost)
	if err != nil {
		return netns.None(), fmt.Errorf("failed to get host veth: %w", err)
	}
	brLink, err := netlink.LinkByName(brName)
	if err != nil {
		return netns.None(), fmt.Errorf("failed to get bridge: %w", err)
	}
	if err := netlink.LinkSetMaster(hostLink, brLink); err != nil {
		return netns.None(), fmt.Errorf("failed to set master: %w", err)
	}

	if err := netlink.LinkSetUp(hostLink); err != nil {
		return netns.None(), fmt.Errorf("failed to bring host veth up: %w", err)
	}

	nsHandle, err = netns.New()
	if err != nil {
		return netns.None(), fmt.Errorf("failed to get ns handle: %w", err)
	}
	defer func() {
		if errNs := netns.Set(currentNs); errNs != nil {
			err = fmt.Errorf("failed to restore netns: %w", errNs)
		}
		if err != nil {
			nsHandle.Close()
		}
	}()
	if err := netns.Set(currentNs); err != nil {
		return netns.None(), fmt.Errorf("failed to set netns to original: %w", err)
	}

	if err := netlink.LinkSetNsFd(peer, int(nsHandle)); err != nil {
		return netns.None(), fmt.Errorf("failed to set peer link to ns: %w", err)
	}

	if err := netns.Set(nsHandle); err != nil {
		return netns.None(), fmt.Errorf("failed to set netns to original: %w", err)
	}

	// Inside namespace: set up eth0
	link, err := netlink.LinkByName(vethNs)
	if err != nil {
		return netns.None(), fmt.Errorf("failed to get link by name: %w", err)
	}

	// Rename to eth0
	if err := netlink.LinkSetName(link, "eth0"); err != nil {
		return netns.None(), fmt.Errorf("failed to set link name: %w", err)
	}

	addr := &netlink.Addr{
		IPNet: nsIP,
	}
	if err := netlink.AddrAdd(link, addr); err != nil {
		return netns.None(), fmt.Errorf("failed to add addr: %w", err)
	}

	if err := netlink.LinkSetUp(link); err != nil {
		return netns.None(), fmt.Errorf("failed to set link up: %w", err)
	}

	route := &netlink.Route{
		Gw: nsGateway,
	}
	if err := netlink.RouteAdd(route); err != nil {
		return netns.None(), fmt.Errorf("failed to add route: %w", err)
	}

	return nsHandle, nil
}

func (nd *NetworkDaemon) setupNftablesNAT(extIface string) error {
	conn := &nftables.Conn{}

	natTable := &nftables.Table{
		Family: nftables.TableFamilyIPv4,
		Name:   "nat",
	}
	conn.AddTable(natTable)
	policy := nftables.ChainPolicyAccept

	postrouting := &nftables.Chain{
		Name:     "postrouting",
		Table:    natTable,
		Type:     nftables.ChainTypeNAT,
		Hooknum:  nftables.ChainHookPostrouting,
		Priority: nftables.ChainPriorityNATSource,
		Policy:   &policy,
	}
	conn.AddChain(postrouting)

	dstPort := 2222
	dnatChain := &nftables.Chain{
		Name:     "prerouting",
		Table:    natTable,
		Type:     nftables.ChainTypeNAT,
		Hooknum:  nftables.ChainHookPrerouting,
		Priority: nftables.ChainPriorityNATDest,
		Policy:   &policy,
	}
	conn.AddChain(dnatChain)

	for _, binding := range nd.pool {
		// forward :<binding.AgentPort> to <binding.Addr>:2222
		conn.AddRule(&nftables.Rule{
			Table: natTable,
			Chain: dnatChain,
			Exprs: []expr.Any{
				&expr.Meta{Key: expr.MetaKeyIIFNAME, Register: 1},
				&expr.Cmp{
					Register: 1,
					Op:       expr.CmpOpEq,
					Data:     append([]byte(extIface), 0x00),
				},
				// [ payload load 2b @ transport header + 2 => reg 1 ]  => TCP dest port
				&expr.Payload{
					DestRegister: 1,
					Base:         expr.PayloadBaseTransportHeader,
					Offset:       2,
					Len:          2,
				},
				&expr.Cmp{
					Register: 1,
					Op:       expr.CmpOpEq,
					Data:     []byte{byte(binding.AgentPort >> 8), byte(binding.AgentPort & 0xFF)},
				},
				&expr.Immediate{
					Register: 1,
					Data:     binding.Addr.To4(),
				},
				&expr.Immediate{
					Register: 2,
					Data:     []byte{byte(dstPort >> 8), byte(dstPort & 0xFF)},
				},
				&expr.NAT{
					Type:        expr.NATTypeDestNAT,
					Family:      unix.NFPROTO_IPV4,
					RegAddrMin:  1,
					RegProtoMin: 2,
				},
			},
		})
	}

	// Masquerade everything going out extIface
	conn.AddRule(&nftables.Rule{
		Table: natTable,
		Chain: postrouting,
		Exprs: []expr.Any{
			&expr.Meta{Key: expr.MetaKeyOIFNAME, Register: 1},
			&expr.Cmp{
				Register: 1,
				Op:       expr.CmpOpEq,
				Data:     append([]byte(extIface), 0x00),
			},
			&expr.Masq{},
		},
	})
	return conn.Flush()
}
