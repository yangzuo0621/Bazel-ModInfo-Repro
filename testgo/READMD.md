
1. Start a server to serve download files

```python
python app.py
```

2. Package rules_go and place it under download folder

Run `./pack-rule_go.sh` and copy the sha256 value of the output, and replace the value in `WORKSPACE` file with `io_bazel_rules_go` rule.
