# Kaggle Upload Instructions

1. Install and configure Kaggle API:
```bash
pip install kaggle
```
Place your Kaggle API token at `~/.kaggle/kaggle.json` and run:
```bash
chmod 600 ~/.kaggle/kaggle.json
```

2. (Optional) Edit `dataset-metadata.json`:
- current id is set to `swadhinbiswas/homomorphic_request`
- adjust title/subtitle/license if needed

3. Create dataset:
```bash
kaggle datasets create -p .
```

4. Create a new version later:
```bash
kaggle datasets version -p . -m "update: new runs"
```
