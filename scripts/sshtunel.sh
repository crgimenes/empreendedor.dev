#!/bin/bash

set -euo pipefail

ssh -N -T \
  -o ExitOnForwardFailure=yes \
  -o ServerAliveInterval=30 \
  -o ServerAliveCountMax=3 \
  -R 3210:localhost:3210 \
  empreendedor.dev

