# Prérequis

**Audience:** Opérateur de plateforme

Cette page liste tout ce dont vous avez besoin avant de commencer la procédure. Chaque ligne renvoie à la page de configuration qui couvre les détails.

| Prérequis | Version minimale | Page de configuration |
|---|---|---|
| Cluster Kubernetes | 1.26+ avec runtime conteneur compatible NRI | [Configuration : Kubernetes](setup-kubernetes.md) |
| Runtime conteneur | containerd ≥ 1.7 avec NRI activé, ou CRI-O ≥ 1.26 | [Configuration : Kubernetes](setup-kubernetes.md) |
| Vault ou OpenBao | Vault ≥ 1.13 / OpenBao ≥ 2.0 | [Configuration : Vault](setup-vault.md) |
| Moteur de base de données | PostgreSQL 13+ (ou MySQL/MariaDB/Oracle — voir notes) | [Configuration : Base de données](setup-database.md) |
| `kubectl` | correspond au mineur du cluster | local |
| `helm` | 3.12+ | local |
| `vault` CLI | correspond au mineur du serveur | local |

## Pourquoi chaque prérequis est important

Kubernetes 1.26+ est le plancher car l'interface du plugin NRI (côté CRI-O) s'y est stabilisée. L'API projected ServiceAccount token est stable depuis la 1.22, mais sans support NRI dans le runtime vous êtes limité au mode webhook legacy.

containerd ≥ 1.7 ou CRI-O ≥ 1.26 est requis pour NRI. Le plugin communique avec le socket NRI de containerd via `/var/run/nri/nri.sock`. Les runtimes plus anciens ne disposent pas de ce socket.

Vault ≥ 1.13 (ou OpenBao ≥ 2.0) est nécessaire pour le champ `audience` de la méthode d'authentification Kubernetes et le chemin d'émission TokenRequest utilisé en mode projected-SA.

La procédure utilise PostgreSQL, mais MySQL, MariaDB et Oracle fonctionnent avec leurs plugins de base de données Vault respectifs. Les exemples SQL diffèrent ; le schéma de configuration Vault est identique.

Installez `kubectl`, `helm` et le CLI `vault` localement et confirmez qu'ils peuvent atteindre vos endpoints cibles avant de continuer.

## Suivant

[Configuration : Cluster Kubernetes](setup-kubernetes.md)
