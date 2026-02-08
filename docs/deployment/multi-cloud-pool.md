# Multi-cloud scaling

Velda can scale across multiple cloud providers and regions, but the recommended and simplest configuration is to keep all worker nodes in the same VPC (so they use private IPs) as the primary agent. This avoids cross-cloud routing complexity and provides the best performance and observability.

If you must span multiple clouds, Velda supports the following common approaches to connect remote workers into the primary VPC network:

## WireGuard + routing

For teams comfortable managing their own VPN tunnels, WireGuard is a lightweight option. A good starting point is the official quickstart and install guides:

- WireGuard quickstart: https://www.wireguard.com/quickstart/
- WireGuard install & tutorials: https://www.wireguard.com/install/

When using WireGuard between clouds, be sure to:
- Configure static routes on each side so the worker subnets are reachable through the tunnel.
- Enable IP forwarding on tunnel hosts and adjust firewall rules to allow forwarded traffic.
- Tune MTU if you observe fragmentation issues.

## Tailscale / Headscale

If you prefer a managed or self-hosted tailnet, Tailscale (or Headscale as a control server) can join remote nodes into a private network and advertise routes back to the primary site. The steps below show a minimal Headscale + Tailscale setup that advertises a route into the tailscale network. Replace `ROUTE` with your Velda controller subnet CIDR (example uses `172.31.16.0/20`).

### Setup a headscale server
You can use tailscale managed control plane, or self-host with head-scale.

To set-up headscale server(can be the same instance as velda apiserver):
```bash

wget https://github.com/juanfont/headscale/releases/download/v0.27.1/headscale_0.27.1_linux_amd64.deb
sudo dpkg -i headscale_0.27.1_linux_amd64.deb

# Open to public
sudo sed -i 's/listen_addr: 127.0.0.1:8080/listen_addr: 0.0.0.0:8080/' /etc/headscale/config.yaml
sudo systemctl restart headscale

# Setup Velda as a user in Headscale
headscale users add velda
PRIMARYKEY=$(headscale preauthkeys create -u 1)
NODEKEY=$(headscale preauthkeys create -u 1 --ephemeral --expiration=3650d --reusable)
```

Make sure your headscale server has public IP and port 8080 is open, and keep a note of $PRIMARYKEY and $NODEKEY

### Join the primary subnet to headscale
Run this from the VPC where velda-controller is located.

```bash
ROUTE=[UPDATEME]
# IP forwarding
echo 'net.ipv4.ip_forward = 1' | sudo tee -a /etc/sysctl.d/99-tailscale.conf
echo 'net.ipv6.conf.all.forwarding = 1' | sudo tee -a /etc/sysctl.d/99-tailscale.conf
sudo sysctl -p /etc/sysctl.d/99-tailscale.conf

# Setup tailscale to join. Run this on a node in the controller VPC, e.g. directly on controller.
curl -fsSL https://tailscale.com/install.sh | sh
# Replace localhost with the headscale server if different instance.
sudo tailscale up --login-server http://localhost:8080 --authkey $PRIMARYKEY --advertise-route=$ROUTE

# Accept routes
headscale node approve-routes -i 1 -r $ROUTE
```

And configure route table for the TailScale CIDR to use the node above. Please refer to the document of your cloud on how to setup the routing.

For optimal performance, enable UDP port 3478 & 41641 for the node from all internet for direct connection via STUN.

### Using `tailscale_config`

Once the tailscale is configure, you may set the tail scale server & key in the pool's backend:
```
# Example 
tailscale_config:
  server: "http://headscale.example.local:8080"   # Use the public IP of the headscale server
  pre_auth_key: "tskey-xxxxxxxxxxxxxxxx" # Use NODEKEY in step above.
```


## Network performance
For optimal performance, it's strongly recommended to:

* [Use snapshot & writable dir](../user-guide/snapshots-and-overlays.md): This avoid the metadata lookup, all files not in writable dir will be cached.
* Use local copy / cache of data sets if possible
* Configure the pool to use suspended instance when scaling down. Suspended instance have cache of the data from snapshot and can be reused for future workloads.


## Troubleshooting
Please refer to documents of tailscale & Wireguard.