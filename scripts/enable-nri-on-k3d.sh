#!/usr/bin/env bash
# Enables NRI on every k3d node of the cluster passed as $1.
# Idempotent. Restarts each node container after patching so k3s/containerd reloads config.
set -euo pipefail

CLUSTER="${1:-vault-db-test}"
TMPL_PATH=/var/lib/rancher/k3s/agent/etc/containerd/config.toml.tmpl

NODES=$(k3d node list --no-headers 2>/dev/null | awk -v c="$CLUSTER" '$3==c && $2!="loadbalancer" {print $1}')

if [ -z "$NODES" ]; then
  echo "No nodes found for cluster '$CLUSTER'" >&2
  exit 1
fi

for NODE in $NODES; do
  echo "Patching $NODE"
  # Render the existing config and append the NRI plugin block.
  docker exec "$NODE" sh -c "
    set -e
    SRC=/var/lib/rancher/k3s/agent/etc/containerd/config.toml
    TMPL=$TMPL_PATH
    if grep -q 'io.containerd.nri.v1.nri' \"\$TMPL\" 2>/dev/null; then
      echo '  already patched, skipping'
      exit 0
    fi
    if [ ! -f \"\$TMPL\" ]; then
      cp \"\$SRC\" \"\$TMPL\"
    fi
    cat >> \"\$TMPL\" <<EOF

[plugins.'io.containerd.nri.v1.nri']
  disable = false
  socket_path = '/var/run/nri/nri.sock'
  plugin_path = '/opt/nri/plugins'
  plugin_config_path = '/etc/nri/conf.d'
  plugin_registration_timeout = '5s'
  plugin_request_timeout = '2s'
EOF
    mkdir -p /var/run/nri /opt/nri/plugins /etc/nri/conf.d
  "
  docker restart "$NODE" >/dev/null
done

echo "Waiting for cluster Ready..."
kubectl wait --for=condition=Ready node --all --timeout=180s

echo "NRI socket check:"
for NODE in $NODES; do
  if docker exec "$NODE" ls -l /var/run/nri/nri.sock >/dev/null 2>&1; then
    echo "  $NODE: socket present"
  else
    echo "  $NODE: SOCKET MISSING" >&2
  fi
done
