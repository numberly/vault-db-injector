# Modes d'injection

**Audience:** Développeur d'application

En tant que développeur, vous ne choisissez pas le mode de livraison — l'opérateur le
définit à l'installation. Ce qui change, c'est ce que `kubectl get pod -o yaml`
affiche par rapport à ce que `kubectl exec -- env` montre à l'intérieur du conteneur
en cours d'exécution. Comprendre la différence est important lorsque les identifiants
n'apparaissent pas là où vous les attendez.

!!! note
    Le "mode de livraison" (NRI vs webhook) et le "mode d'annotation" (`classic`
    vs `uri`) sont orthogonaux. Vous choisissez le mode d'annotation via
    `<dbname>.mode` ; l'opérateur contrôle le mode de livraison. Les deux
    modes d'annotation fonctionnent avec les deux modes de livraison.

## Mode webhook (legacy)

En mode webhook, l'injector récupère les identifiants réels depuis Vault
lors de l'admission du pod et les écrit directement dans les variables
d'environnement du pod avant que le PodSpec ne soit stocké.

Ce que vous voyez dans le pod en cours d'exécution :

```bash
$ kubectl -n team-myapp get pod myapp -o yaml | grep -A5 env:
        env:
        - name: DB_USER
          value: vault-myapp-prod-1746345600-x8k2j9ab
        - name: DB_PASS
          value: A1B2-c3d4-E5F6-g7h8
```

Le nom d'utilisateur et le mot de passe réels sont visibles dans le PodSpec, dans etcd,
et dans les journaux d'audit Kubernetes.

## Mode NRI (canonique)

En mode NRI, le webhook place des chaînes placeholder opaques dans les variables
d'environnement lors de l'admission. Un plugin NRI local au nœud intercepte `CreateContainer`
et substitue les identifiants réels avant l'exécution de `runc` — le processus applicatif
démarre avec les vraies valeurs déjà présentes dans son environnement.

Ce que `kubectl get pod -o yaml` affiche :

```bash
$ kubectl -n team-myapp get pod myapp -o yaml | grep -A5 env:
        env:
        - name: DB_USER
          value: __VDBI_PH_3a7f1c9b2e4d6a8f0b1c3d5e7f9a2b4c6d8e0f1a3b5c7d9e1f3a5b7c9d1e3f5a___
        - name: DB_PASS
          value: __VDBI_PH_8e2f4a6c0b8d2e4f6a8c0d2e4f6a8c0d2e4f6a8c0d2e4f6a8c0d2e4f6a8c0d2e___
```

Ce que `kubectl exec -- env` affiche à l'intérieur du conteneur en cours d'exécution :

```bash
$ kubectl -n team-myapp exec myapp -- env | grep DB_
DB_USER=vault-myapp-prod-1746345600-x8k2j9ab
DB_PASS=A1B2-c3d4-E5F6-g7h8
```

Les identifiants réels n'apparaissent jamais dans aucune ressource Kubernetes persistée.

## Comment identifier le mode actif

```bash
kubectl -n team-myapp get pod myapp -o yaml | grep VDBI_PH
```

Un résultat positif indique que le mode NRI est actif. Aucun résultat indique le mode webhook (legacy).

## Dépannage des chaînes placeholder dans le conteneur en cours d'exécution

Si `kubectl exec -- env` affiche des chaînes placeholder plutôt que des identifiants
réels, le plugin NRI sur ce nœud n'a pas effectué la substitution. Causes fréquentes :

- Le pod du DaemonSet du plugin n'est pas en cours d'exécution sur le nœud où le pod a été planifié.
- Le pod a été admis sans le label `vault-db-injector: "true"`, puis le label a été ajouté après l'admission (les labels ne sont lus qu'au moment de l'admission).
- Le nœud n'était pas enregistré avec containerd NRI.

Voir [troubleshooting](troubleshooting.md) pour les étapes de diagnostic.
