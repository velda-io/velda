## Train your first model on Velda Cloud

This short guide shows how to launch your first model training job on [Velda Cloud](https://cloud.velda.io) in under two minutes.

### 1. Sign in or Register on Velda Cloud

Go to [https://cloud.velda.io](https://cloud.velda.io) and sign in with your Velda account.

### 2. Create a Velda instance (PyTorch template)

A Velda instance is a virtual environment with dynamic compute resource.

Click the **PyTorch 2.10** template to create a pre-configured instance that includes a ready-to-run example and the necessary dependencies.

![PyTorch template](../../assets/pytorch.png)

### 3. Connect to your instance

After the instance is created, click **Connect**. Velda launches a Code-server session in your browser with a default workspace containing example projects.

![Connect to instance](../../assets/connect.png)

This workspace is yours: edit code, install packages, or open the project in your preferred IDE using SSH or the provided connection details.

### 4. Run the example training job

Open the terminal in Code-server (`` Ctrl+` `` on Windows/Linux, `` Control+` `` on macOS) and run:

```bash
vrun -P h100-1 pytorch_examples/example_train.py
```

You should see the training job start and job output in the terminal. This training job will be running on H100 GPU instance.

![training run](../../assets/h100-run.gif)

### What’s next

- To run custom scripts, replace `pytorch_examples/example_train.py` with your script path.
- Explore [more templates](https://velda.cloud/image-catalog), or set up your own from the Ubuntu templates or a custom container image.
- Explore running with other GPU models, check [pricing](https://velda.io/pricing) for more pool options.
- Use [`vrun --help`](../../reference/cli/velda_run/) to see available flags and placement options.