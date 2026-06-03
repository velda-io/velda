## Run NVIDIA Isaac Sim on Velda Cloud

NVIDIA Isaac Sim is a robotics simulation platform used to build and test physical AI workflows in realistic 3D environments. It is widely used for robotics development, synthetic data generation, and digital twin simulation.

For full product details, see the official NVIDIA page: [NVIDIA Isaac Sim](https://developer.nvidia.com/isaac/sim).

With Velda, you can launch Isaac Sim on a serverless GPU in about a minute, and access directly through your browser.

![Isaac sim](https://assets.velda.io/isaac.jpg)

## Quick Start

### 1. Clone the Isaac Sim template
Find [`NVIDIA Isaac SIM`](https://velda.cloud/image/nvidia-isaac-sim-5-1) in the image catalog. Create a new instance from the template.

### 2. Start the simulator

```bash
# Install Velda CLI
curl https://velda.cloud/install.sh | bash

# Start the simulator
velda run -s isaac -P l40s-1-16d -i <instance-name> sh start.sh
```

This starts Isaac Sim on an NVIDIA L40S GPU pool in the cloud.
It will include Xorg server, XFCE window manager, VNC for remote desktop, noVNC/websockify for web access, and Isaac Sim.

Please note GPU rendering is not supported on Ampere / Hopper / Blackwell.

### 3. Open Isaac Sim in your browser

When the command starts, it prints a URL in the terminal. Open that URL to access the simulator directly in your browser.

No desktop app install is needed.

## Learn more

- [Isaac Sim document](https://docs.isaacsim.omniverse.nvidia.com/5.1.0/index.html)
- [Source code](https://github.com/velda-io/examples/blob/main/setup_xorg.sh) for setting up Xorg with NVIDIA rendering and remote VDI