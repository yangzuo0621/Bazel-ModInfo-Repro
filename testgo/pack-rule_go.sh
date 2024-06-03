#!/usr/bin/env bash

set -ex

CURRENT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"

RULES_GO_DIR="$CURRENT_DIR/../rules_go"
TARGET="$CURRENT_DIR/downloads/rules_go.zip"
pushd "$RULES_GO_DIR"
    zip -r "$TARGET" ./* 
popd

sha256sum $TARGET

# go build -a -x .
# /home/yangzuo/workspaces/projects_go/testgo/bazel-testgo/bazel-out/k8-fastbuild/bin/testgo_/testgo
# https://github.com/bazelbuild/rules_go/issues/3090#issuecomment-1439765106