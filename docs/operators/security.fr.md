# Sécurité

**Audience:** Opérateur de plateforme

## Modèle de menace

Les menaces à considérer se répartissent en trois catégories : un pod
compromis, un nœud compromis, et un injector compromis. Un **pod** compromis
peut lire ses propres variables d'environnement et `/proc/<pid>/environ`. En
mode legacy, cela expose les identifiants DB en clair ; en mode NRI, cela
expose des placeholders opaques avant substitution et les mêmes identifiants
en clair après substitution. Un **nœud** compromis avec accès root contrôle
déjà tous les conteneurs dessus — root peut lire chaque `/proc/*/environ` et
chaque Secret monté. Un **injector** compromis est celui dont le comportement
change selon le mode.

En **mode legacy**, l'injector détient une politique Vault étendue :
`database/creds/*` et `auth/token/create-orphan`. Un attaquant qui prend le
contrôle du pod injector peut émettre des identifiants pour n'importe quel
rôle DB configuré sous `database/`. Le rayon d'impact est l'union de tous les
moteurs DB que l'injector peut atteindre.

En **mode projected-SA**, l'injector ne détient pas de politique d'émission DB.
Le pod-token porte la contrainte de rôle de manière cryptographique : un JWT
émis par l'injector pour le pod A ne peut pas passer `bound_service_account_names`
sur le rôle Vault du pod B. Un injector compromis peut toujours émettre des
TokenRequests pour le ServiceAccount de n'importe quel pod — mais le token
Vault résultant est limité au rôle du pod, et le journal d'audit Vault attribue
l'émission au pod réel.

Risques résiduels : le **DaemonSet du plugin NRI s'exécute en root** pour lire
le socket containerd. Une évasion de conteneur depuis le pod du plugin donne un
accès root complet au nœud et (via la permission `serviceaccounts/token` à
l'échelle du cluster) un accès Vault effectivement total. Le mode NRI ne
mitige pas la compromission au niveau du nœud — il mitige la fuite des
identifiants depuis etcd, GitOps, et les journaux d'audit.

## Durcissement du mode NRI

- **PodSecurityAdmission `restricted`** sur chaque namespace utilisateur.
  `restricted` et `baseline` interdisent tous deux les volumes hostPath, seul
  moyen pour un pod non-injector de s'enregistrer comme plugin NRI ou de lire
  le fichier de cache. Le namespace propre du plugin doit être labélisé
  `pod-security.kubernetes.io/enforce=privileged`.
- **Kyverno ClusterPolicy** bloquant les montages hostPath `/var/run/nri`,
  `/opt/nri`, et `/run/<release-fullname>` en dehors du namespace de confiance.
  Politique de référence ci-dessous.
- **SELinux/AppArmor en mode enforcing** sur RHEL/CoreOS. N'exécutez aucun pod
  avec `seLinuxOptions.type: spc_t`. Le label de socket
  `container_runtime_t` par défaut empêche les pods utilisateurs de se
  connecter même s'ils contournent la vérification hostPath.
- **Restrictions hostPath** au niveau de l'admission : le seul consommateur
  légitime de `/var/run/nri/nri.sock` est le DaemonSet du plugin lui-même.

## Politique Kyverno

Le dépôt fournit une Kyverno ClusterPolicy de référence qui bloque les montages
hostPath NRI en dehors du namespace de l'injector. C'est la couche de défense
en profondeur la moins coûteuse au-dessus de PSA, et la base recommandée :

[helm/policies/kyverno-restrict-nri-socket.yaml](https://github.com/numberly/vault-db-injector/blob/main/helm/policies/kyverno-restrict-nri-socket.yaml)

La politique refuse tout pod qui monte `/var/run/nri`, `/opt/nri`, ou
`/run/<release-fullname>` sauf s'il se trouve dans le namespace de l'injector.
Cela ferme le vecteur "pod utilisateur qui s'enregistre comme plugin NRI",
déjà bloqué par PSA au niveau `restricted` — utile quand vous ne pouvez pas
imposer `restricted` à l'échelle du cluster.

## Gains de sécurité du mode projected-SA

- **Attestation native par Vault** : le journal d'audit indique quel ServiceAccount
  de pod a acquis quels identifiants. En mode legacy, chaque émission est
  attribuée au ServiceAccount de l'injector.
- **Un injector compromis ne peut pas émettre d'identifiants DB arbitraires** :
  l'injector n'a pas de politique d'émission DB en mode projected, et le
  pod-token porte la contrainte de rôle de manière cryptographique.
- **Rayon d'impact réduit** : la seule capacité Kubernetes dont l'injector a
  encore besoin est `serviceaccounts/token`, limitée par audience. Un
  `tokenRequestAudiences` vide est refusé au démarrage depuis la v3.0 car
  une audience vide produit un JWT réutilisable par n'importe quel service.

## Posture du fichier de cache

Le cache du plugin NRI à `/run/<release-fullname>/nri/cache.json` contient
des identifiants déchiffrés en clair, avec les permissions `0600 root:root`,
sur tmpfs. La même posture s'applique aux tokens ServiceAccount projected de
kubelet à `/var/lib/kubelet/pods/<UID>/volumes/kubernetes.io~projected/...`
et à tout Secret monté comme volume.

Un attaquant root sur le nœud peut déjà lire `/proc/<pid>/environ` de chaque
conteneur, donc le cache n'ajoute aucune nouvelle surface d'attaque au-delà de
ce que root possède déjà. Le cache n'est **jamais sur disque persistant**
(tmpfs) et **jamais dans les sauvegardes** (`/run` est exclu de tous les
outils de sauvegarde de nœud).

Le seul vecteur permettant à un pod utilisateur non-root de lire le cache est
de monter hostPath `/run` et de s'exécuter en UID 0. PSA `restricted` et
`baseline` interdisent les montages hostPath. La politique Kyverno bloque
`/run/<release-fullname>` pour les pods utilisateurs comme second niveau de
protection.

## Piste d'audit

En **mode projected**, le journal d'audit Vault affiche le ServiceAccount du
pod comme identité ayant émis chaque appel `database/creds/<role>`. Corrélation
par nom de ServiceAccount et annotation `db-creds-injector.numberly.io/uuid`
inscrite sur le pod. En **mode legacy**, chaque émission est attribuée au
ServiceAccount propre de l'injector — le journal d'audit indique que l'injector
a agi pour le compte de quelqu'un, mais ne peut pas prouver qui sans corréler
avec le mont de bookkeeping KV.

!!! danger "Le DaemonSet NRI s'exécute en root"
    Le mode NRI requiert que le DaemonSet du plugin monte
    `/var/run/nri/nri.sock` — le même socket que containerd utilise pour
    l'enregistrement des plugins. Tout pod qui monte ce hostPath peut
    s'enregistrer comme plugin NRI et muter tous les conteneurs créés sur
    le nœud (variables d'environnement, montages, capabilities, arguments).

    Ceci est **inhérent à NRI**, pas spécifique à ce projet. L'administrateur
    du cluster doit restreindre qui peut monter ces chemins via PSA
    `restricted`/`baseline` sur les namespaces utilisateurs, la politique
    Kyverno ci-dessus, et SELinux/AppArmor en mode enforcing. Une évasion de
    conteneur depuis le pod du plugin NRI donne un accès root complet au nœud
    et un accès Vault effectivement total via la permission
    `serviceaccounts/token` à l'échelle du cluster.

    Déployez le mode NRI uniquement sur des nœuds dédiés ou durcis. Vérifiez
    régulièrement les images de nœud pour détecter toute compromission.
