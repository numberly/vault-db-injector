# Architecture

**Audience:** Opérateur de plateforme

## Vue d'ensemble du système

vault-db-injector est composé de quatre composants coopérants : le **webhook
d'injection**, le **DaemonSet du plugin NRI**, le **renewer**, et le
**revoker**. Ils partagent le même binaire Go et sélectionnent leur rôle au
démarrage via la clé de configuration `mode`. Ils dépendent de deux systèmes
externes : Vault (ou OpenBao) pour l'émission et la gestion des identifiants,
et l'API server Kubernetes pour les événements d'admission et de pod-watch.
Le webhook est la porte d'admission. Le plugin NRI substitue les identifiants
dans les conteneurs au dernier moment avant runc. Le renewer maintient les
tokens et les baux actifs. Le revoker les révoque à la mort du pod.

## Diagramme

```
                       ┌──────────────────┐
                       │  Vault / OpenBao │
                       │  auth/kubernetes │
                       │  database/...    │
                       │  KV bookkeeping  │
                       └────────┬─────────┘
                                │
        ┌───────────────────────┼─────────────────────┐
        │                       │                     │
   write/read KV           renew tokens         revoke tokens
   issue pod-token          + leases              + leases
        │                       │                     │
┌───────┴─────────┐    ┌────────┴───────┐    ┌────────┴────────┐
│ Injector        │    │  Renewer       │    │  Revoker        │
│ (Deployment)    │    │  (Deployment)  │    │  (Deployment)   │
│ webhook server  │    │  periodic      │    │  pod-watch +    │
│                 │    │  ticker        │    │  safety-net     │
└───────┬─────────┘    └────────────────┘    └─────────────────┘
        │
        │ admit pod (placeholders only in NRI mode)
        │
┌───────┴─────────┐                          ┌──────────────────┐
│  kube-apiserver │◄────── pod-watch ────────│  K8s API events  │
└───────┬─────────┘                          └──────────────────┘
        │
        │ schedule
        ▼
   ┌──────────┐
   │  kubelet │
   └─────┬────┘
         │
         │ /var/run/nri/nri.sock
         ▼
┌────────────────────────┐    on CreateContainer:
│  NRI plugin            │      substitute placeholders
│  (DaemonSet, root,     │      with real DB credentials
│   per node)            │      from Vault
└────────┬───────────────┘
         │
         ▼
       runc → container starts with real envp
```

## Flux de données

1. Un pod utilisateur portant le label `vault-db-injector: "true"` est soumis
   à l'API server.
2. Le webhook intercepte l'admission. Il valide le RBAC Vault via
   `CanIGetRoles` (mode legacy) ou s'appuie sur l'attestation native de Vault
   au moment du pod-token (mode projected), puis écrit soit des identifiants
   en clair, soit des placeholders `__VDBI_PH_<64hex>___` dans les variables
   d'environnement du pod.
3. Le pod est planifié. En mode NRI, le plugin par nœud reçoit un événement
   `CreateContainer` avant runc, récupère les identifiants réels depuis Vault
   via un JWT TokenRequest par pod, et émet un `ContainerAdjustment` qui
   substitue les placeholders.
4. Le conteneur démarre avec des identifiants valides dans son environnement.
5. Le renewer s'exécute toutes les 5 minutes (configurable). À chaque cycle,
   le leader parcourt le mont de bookkeeping KV et renouvelle chaque pod-token
   ainsi que son bail DB.
6. Lorsque le pod est supprimé, le pod-watch du revoker se déclenche. Le token
   et le bail sont révoqués, l'entrée KV est effacée. Un balayage périodique
   de filet de sécurité rattrape ce que le watch a pu manquer.

## Frontières de confiance

- L'**injector** détient un token Vault limité au bookkeeping KV, ainsi que
  (mode legacy uniquement) `database/creds/*` et `auth/token/create-orphan`.
- En mode projected, le **plugin NRI** ne détient pas d'identité Vault
  persistante au-delà de ce qui est nécessaire pour appeler TokenRequest pour
  le ServiceAccount du pod. Il se connecte à Vault par pod, en tant que pod.
- Le **renewer** et le **revoker** en mode projected disposent chacun d'un
  ServiceAccount dédié lié à une politique Vault minimale : renew uniquement
  pour le renewer, revoke uniquement pour le revoker.

Pour la vue par composant, voir [components](components.md). Pour le modèle de
menace, voir [security](security.md).
