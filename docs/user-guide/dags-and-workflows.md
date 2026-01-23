
# Building Scalable Workflows with DAGs

In many projects, building a robust and scalable workflow is as crucial as the application itself. A well-structured pipeline ensures reproducibility and speeds up development and deployment cycles. Velda simplifies building complex workflows through its command-line tools that handle cloud resource management automatically.


## Simple Machine Learning Workflow

Let's build a simple workflow with three stages: data processing, computation, and validation. We can chain these tasks together using the `--after-success` flag, which ensures a task starts only after its dependencies have successfully completed.

Create a file called `training_pipeline.sh`:

```bash
#!/bin/bash

# Process the data, e.g. data cleaning
vbatch -P cpu-16 --name process python process_data.py

# Train the model after processing is done
vbatch -P gpu-1 --name train --after-success process python train_model.py

# Model evaluation after training is complete
vbatch -P cpu-8 --name eval --after-success train python evaluate_model.py
```

This creates a linear pipeline where each step is executed in sequence. To execute the pipeline, run:

```bash
vbatch ./training_pipeline.sh
```

All tasks created within a `vbatch` script will be automatically grouped as sub-tasks, and the parent task is only marked as complete when all children tasks complete. This provides a hierarchical view in the web UI.

### Understanding Dependency Flags

Velda provides three types of dependency flags:

- **`--after-success TASK_NAME`**: Run only if the specified task succeeds (exit code 0)
- **`--after-fail TASK_NAME`**: Run only if the specified task fails (non-zero exit code)
- **`--after TASK_NAME`**: Run after the task completes, regardless of success or failure

You can specify multiple dependencies by repeating the flag:

```bash
vbatch --name aggregate --after-success task1 --after-success task2 python aggregate.py
```

## Batch Processing: Parallel Fan-Out

In many real-world scenarios, you'll need to process a large number of data files. This is where the "fan-out" pattern comes in handy. We can use standard bash commands like `xargs` or a `for` loop to process all files from a source in parallel.


For example, to process all .csv files in a directory:

```bash
vbatch bash -c "ls *.csv | xargs -I {} vbatch --name {} -P cpu-8 python process_file.py {}"
```


This command launches a separate task for each .csv file, processing them in parallel. Since it's wrapped under one top-level `vbatch` command, every individual command is grouped under one parent task for better organization and searchability.

### Optimizing Task Overhead

Keep in mind that there's always some overhead for starting a task (about 1 second), so we recommend that each task runs for at least one minute. If needed, you can chunk the inputs:

```bash
vbatch bash -c "ls *.csv | xargs -L 100 vbatch -P cpu-8 python process_file.py"
```

This automatically groups up to 100 files in each task and reduces scheduling overhead.


## Embedding Fan-Out in a Pipeline

Now, let's embed the fan-out step into a larger pipeline. We can create a bash script that contains the fan-out logic and then execute that script as a step in our pipeline.

Create `process_all.sh`:

```bash
#!/bin/bash

ls *.csv | xargs -I {} vrun -P cpu-8 python process_file.py {}
```

Now, we can incorporate this into our main pipeline:

```bash
#!/bin/bash

# Process all data files in parallel
vbatch -P cpu-16 --name preprocess ./process_all.sh

# Train the model after all files are processed
vbatch -P gpu-1 --name train --after-success preprocess python train_model.py

# Validate results
vbatch -P cpu-8 --name validate --after-success compute python validate_job.py
```

The pipeline above will start `train` only if all pre-processing tasks have been completed. With that, you can process thousands of datasets without any complex orchestration tools.

## More Complex Pipelines: Recursive Fan-Outs

For more complex scenarios, you can define recursive pipelines within the fan-out pattern, or any hierarchy that you need. For example, for each data point, you might want to run inference, evaluate the result, and then aggregate the evaluations.

Create a "sub-pipeline" script `inference_and_eval.sh` that takes a data file as input:

```bash
#!/bin/bash
DATA_FILE=$1

# Run inference
vrun -P gpu-1 --name inference ./run_inference.py $DATA_FILE

# Evaluate the inference result
vrun -P cpu-4 --after-success inference ./evaluate_inference.py $DATA_FILE
```

Now, use this script in your fan-out:

```bash
vbatch bash -c "ls *.csv | xargs -I {} vbatch --name {} ./inference_and_eval.sh {}"
```

This creates a two-level pipeline for each data file. The power of this approach is that you can build arbitrarily complex and recursive workflows that remain easy to manage and scale.

### Task Hierarchy

When tasks are created within a `vbatch` command, they form a parent-child relationship. The web UI displays this hierarchy, making it easy to navigate complex workflows.

## Best Practices

### 1. Name Your Tasks

Always use `--name` for tasks to make them easier to identify:

```bash
# Good: Named task
vbatch --name preprocess-dataset python process.py

# Less ideal: Unnamed task
vbatch python process.py
```

### 2. Use Appropriate Pools

Match compute resources to workload requirements:

```bash
vbatch --name data-prep -P cpu-16 python prepare.py      # CPU-intensive
vbatch --name training -P gpu-a100 python train.py       # GPU training
vbatch --name evaluation -P cpu-4 python evaluate.py     # Light evaluation
```

### 3. Minimize Task Overhead

Each task has ~1 second of overhead. For very short operations, batch them together:

```bash
# Good: Batch processing
ls *.csv | xargs -L 100 vbatch -P cpu-8 python process.py

# Less efficient: Too many small tasks
ls *.csv | xargs -I {} vbatch -P cpu-8 python process.py {}  # if each takes <1 min
```

### 4. Use Scripts for Complex Workflows

For multi-step workflows, create bash scripts and use sub-tasks to manage individual steps

```bash
# workflow.sh
#!/bin/bash
vbatch --name step1 python step1.py
vbatch --name step2 --after-success step1 python step2.py
vbatch --name step3 --after-success step2 python step3.py
```

Then run:

```bash
vbatch ./workflow.sh
```
