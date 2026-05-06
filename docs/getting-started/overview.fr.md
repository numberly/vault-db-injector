# Vue d'ensemble

**Audience:** Tout le monde

vault-db-injector émet des identifiants de base de données éphémères depuis HashiCorp Vault (ou OpenBao) et les délivre aux pods Kubernetes à l'exécution. Il gère le cycle de vie complet des identifiants : émission au démarrage du pod, renouvellement périodique pendant l'exécution, et révocation à la mort du pod. Les applications lisent les identifiants depuis des variables d'environnement ; elles n'appellent jamais Vault directement.

Ce guide suit le chemin **NRI + Projected-SA**, l'approche recommandée pour les nouveaux déploiements depuis la v3.0. Dans ce mode, les identifiants n'apparaissent jamais dans le PodSpec, etcd, ou les journaux d'audit. Le webhook place des placeholders opaques dans l'environnement du pod ; un plugin NRI local au nœud substitue les valeurs réelles à la création du conteneur, avant le démarrage du processus. Vault authentifie chaque pod via son propre ServiceAccount, de sorte que le journal d'audit indique quelle identité de pod a acquis quels identifiants — pas une identité d'injector partagée.

## Ce que vous allez obtenir

- Vault configuré avec les mounts, politiques et rôles requis pour NRI + Projected-SA
- Cluster Kubernetes vérifié pour le support NRI ; namespace de l'injector créé
- Serveur de base de données préparé avec un compte admin Vault et le rôle owner
- vault-db-injector installé via Helm avec NRI et Projected-SA activés
- Un pod injecté fonctionnel dont l'application lit les vrais identifiants de base de données depuis les variables d'environnement

## Durée estimée

60–90 minutes pour quelqu'un disposant déjà des prérequis (une instance Vault accessible, un cluster actif et un serveur de base de données).

## Suivant

[Prérequis](prerequisites.md)
