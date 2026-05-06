# Configuration : Cluster Kubernetes

**Audience:** Opérateur de plateforme

## Capacités requises du cluster

### Mutating Admission Webhooks

Les mutating admission webhooks sont activés par défaut dans toutes les distributions Kubernetes modernes (kubeadm, EKS, GKE, AKS, k3s, et autres). Aucune action n'est requise sauf si votre cluster a été démarré avec `--disable-admission-plugins=MutatingAdmissionWebhook`.

Vérification :

```bash
kubectl api-versions | grep admissionregistration
```

La sortie attendue inclut `admissionregistration.k8s.io/v1`.

### NRI dans containerd

Ouvrez `/etc/containerd/config.toml` sur chaque nœud et confirmez que la section NRI existe et n'est pas désactivée :

```toml
[plugins."io.containerd.nri.v1.nri"]
  disable = false
  disable_connections = false
  plugin_registration_timeout = "5s"
  plugin_request_timeout = "2m"
  socket_path = "/var/run/nri/nri.sock"
  state_dir = "/var/run/nri"
```

Si la section est absente, ajoutez-la et redémarrez containerd :

```bash
sudo systemctl restart containerd
```

Pour CRI-O, le support NRI est intégré depuis la version 1.26 et ne nécessite aucune modification de configuration.

### Pod Security Admission pour le namespace de l'injector

Le DaemonSet NRI doit monter le socket NRI de containerd depuis l'hôte. Le niveau PSA `restricted` bloque les montages `hostPath`, donc le namespace de l'injector doit utiliser `privileged`.

## Vérifier NRI

Exécutez ces vérifications avant de continuer.

Confirmez que les nœuds sont accessibles :
```bash
kubectl get nodes -o wide
```

Vérifiez que le socket NRI existe sur chaque nœud (exécutez directement sur le nœud, ou via un pod de débogage) :
```bash
ls /var/run/nri/nri.sock
```

Si le chemin est absent, NRI de containerd est désactivé ou la version du runtime est trop ancienne.

Si vous avez `nerdctl` sur le nœud :
```bash
nerdctl --namespace=k8s.io system info | grep -i nri
```

Sortie attendue :
```
nri:
  disable: false
  socket_path: /var/run/nri/nri.sock
```

Si la sortie est vide, NRI est désactivé dans containerd. Consultez la documentation containerd pour activer NRI.

## Créer le namespace de l'injector

```bash
kubectl create namespace vault-db-injector
kubectl label namespace vault-db-injector \
    pod-security.kubernetes.io/enforce=privileged \
    pod-security.kubernetes.io/enforce-version=latest
```

!!! warning "Pourquoi privileged ?"
    Le DaemonSet NRI s'exécute en tant que root et monte `/var/run/nri/nri.sock` depuis l'hôte. Il s'agit du socket containerd pour le protocole NRI — sans lui, le plugin ne peut pas intercepter la création des conteneurs. Les namespaces des workloads utilisateurs restent à `restricted` ou `baseline` ; seul le namespace de l'injector a besoin de `privileged`.

## Suivant

[Configuration : Vault](setup-vault.md)
