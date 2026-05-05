# Glossaire

**Audience:** Tout le monde

| Terme | Définition |
|---|---|
| KV mount | Mount KV-v2 de Vault qui contient les métadonnées par pod : lease ID, token ID, namespace, UUID. Clé Helm : `vaultSecretName` (défaut : `vault-injector`). |
| Vault auth path | Chemin de mount de la méthode d'authentification Kubernetes sur Vault. Clé Helm : `vaultAuthPath` (défaut : `kubernetes`). |
| Injector role | Rôle Vault utilisé par le binaire injector pour se connecter. Clé Helm : `kubeRole`. |
| Database backend | Moteur de secrets `database` de Vault qui émet des identifiants dynamiques pour les connexions de base de données configurées. |
| Database connection | Configuration par serveur de base de données enregistrée sous `database/config/<name>` dans Vault. Indique à Vault comment se connecter et quel compte administrateur utiliser. |
| Database role | Rôle Vault sous `database/roles/<name>` qui définit les instructions SQL utilisées pour créer/révoquer les identifiants d'une application donnée. |
| App role | Rôle `auth/kubernetes` de Vault associé au ServiceAccount d'une application. Contrôle quels rôles de base de données l'application peut demander comme identifiants. |
| `token_period` | Attribut de rôle Vault qui rend le token émis renouvelable périodiquement au-delà de `token_max_ttl`. Obligatoire en mode projected-SA — sans lui, le token du pod meurt à `token_max_ttl` et les identifiants ne peuvent plus être renouvelés. |
| Projected-SA | Mode d'authentification Vault où l'injector émet un JWT Kubernetes TokenRequest pour le ServiceAccount du pod admis et l'utilise pour se connecter à Vault au nom du pod. Le journal d'audit Vault enregistre alors l'identité du SA du pod, pas celle de l'injector. |
| NRI mode | Mode de livraison des identifiants où le webhook écrit des chaînes placeholder dans le PodSpec. Un plugin NRI local au nœud (DaemonSet) substitue les placeholders par les vrais identifiants à la création du conteneur, avant que runc ne démarre le processus. Les identifiants en clair n'apparaissent jamais dans le PodSpec ni dans etcd. |
| Placeholder | Chaîne opaque de la forme `__VDBI_PH_<64hex>___` insérée dans les valeurs de variables d'env par le webhook en mode NRI. |
| Bookkeeping token | Le propre token Vault de l'injector, utilisé pour écrire les métadonnées par pod (lease ID, token ID) dans le mount KV. Distinct du pod-token en mode projected-SA. |
| Pod-token | En mode projected-SA, le token Vault par pod émis via un JWT Kubernetes TokenRequest. Limité au rôle de base de données du pod uniquement. |
| Lease | Lease Vault qui couvre un ensemble d'identifiants de base de données dynamiques. Le renewer le prolonge ; le revoker le termine à la suppression du pod. |
| Orphan token | Token Vault sans token parent. Utilisé en mode legacy pour qu'une révocation du token de l'injector ne se propage pas à tous les tokens des pods. |
| NRI | Node Resource Interface — une API de plugin pour containerd (≥ 1.7) et CRI-O (≥ 1.26) permettant d'intercepter les événements du cycle de vie des conteneurs. vault-db-injector s'enregistre comme plugin NRI pour intercepter `CreateContainer`. |
| `uuid` | UUID par dbConfig défini par le webhook sur chaque pod admis. Sert de clé dans le mount KV et dans tous les labels des métriques `vdbi_*`. Défini automatiquement ; ne pas l'écrire manuellement. |
