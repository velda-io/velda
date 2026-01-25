<p align="center">
  <a href="https://velda.io">
    <picture>
      <img alt="Velda.io" src="https://raw.githubusercontent.com/velda-io/velda/docreadme/docs/logo.png" width=55%>
    </picture>
  </a>
</p>

<p align="center">
  <a href="https://docs.velda.io/">
    <img alt="Documentation" src="https://img.shields.io/badge/docs-gray?logo=readthedocs&logoColor=f5f5f5">
  </a>

  <a href="https://github.com/velda-io/velda/releases">
    <img alt="GitHub Release" src="https://img.shields.io/github/release/velda-io/velda.svg">
  </a>

  <a href="https://discord.gg/MJQbeE33">
    <img alt="Join Slack" src="https://img.shields.io/badge/Velda-Join%20Discord-blue?logo=discord">
  </a>

</p>

<h3 align="center">
    Scale like your local machine in the cloud
</h3>

Containers make apps scalable, but the workflow is broken: Show to build; Dependencies drift; Registry bloat; Complex Job manifests.

Velda eliminates all these steps, and makes your dev-environment immediately scalable:

* ❌ No Dockerfiles or Manifests
* ❌ No image builds or registries  
* ❌ No redeploys on code changes  
* ✅ Instant scale from local dev to cloud compute, run in any cloud

Prefix any command with `vrun` to execute it on cloud resources using the exact same code, data, and dependencies as your development machine.
You may also use `vbatch` or other commands to orchestrate large scale pipelines.

<img src="https://media.licdn.com/dms/image/v2/D5622AQHSKDuq7DUkMA/feedshare-shrink_1280/B56ZuLnXzBKEAs-/0/1767573913380?e=1770854400&v=beta&t=ScfCep-AEomIs4yqvt0zooszf9UbKJo815xxv9qA2m4" />

Velda is designed for:
- ML engineers iterating on training & evaluation
- Data engineers running ad-hoc or scheduled batch jobs
- Infra teams tired of rebuilding containers for every code change

# Getting started
1. [Deploy Velda cluster](https://docs.velda.io/deployment/overview/).
2. Connect to your instance using ssh or Velda CLI and start development.
3. Run jobs with `vrun`, `vbatch`
```
vrun -P gpu-h200-8 python train.py --epochs 100
```
4. Onboard more develoers by cloning from an existing templates.

# 🤝 Contributing
We love contributions from our community ❤️. Pull requests are welcome!

# 👥 Community
We are super excited for community contributions of all kinds - whether it's code improvements, documentation updates, issue reports, feature requests, or discussions in our Discord.

Join our community here:

* 🌟 [Star us on GitHub](https://github.com/velda-io/velda)
* 👋 [Join our Discord community](https://discord.gg/MJQbeE33)
* 📜 [Subscribe to our blog posts](https://blog.velda.io)

# Learn more
Check out [velda.io](https://velda.io) to learn more about Velda and our hosted/enterprise offerings.
