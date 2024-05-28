# Configuration

Here is the configuration for Vault Injector:

#  1 <a name='ModeInjector'></a>Mode Injector
The Injector mode is basic one that will handle all api-server request and handle all requests to vault to generate credentials to our DB
The config file path can be parse by adding the path with : `- "--config=/injector/config.yaml"`
```yaml
certFile: /tls/tls.crt
keyFile: /tls/tls.key
vaultAddress: https://vault1.tld:8200
vaultAuthPath: pgsql2-dv-kubernetes1-dv-par5
logLevel: info
kubeRole: all-rw
tokenTTL: 768h
vaultSecretName: vault-injector
vaultSecretPrefix: kubernetes1-dv-par5
mode: injector
sentry: true
sentryDsn: https://my-sentry-url@sentry.tld/660
injectorLabel: vault-db-injector
defaultEngine: databases
```

#  1 <a name='Modetoken-renewer'></a>Mode token-renewer
The Renewer one is a process that will run every hour and validate that all orphan token won't expire before pod is deleted 
The config file path can be parse by adding the path with : `- "--config=/renewer/config.yaml"`
```yaml
vaultAddress: https://vault1.tld:8200
vaultAuthPath: pgsql2-dv-kubernetes1-dv-par5
logLevel: info
kubeRole: all-rw
tokenTTL: 768h
vaultSecretName: vault-injector
vaultSecretPrefix: kubernetes1-dv-par5
mode: renewer
sentry: true
sentryDsn: https://my-sentry-url@sentry.tld/660
SyncTTLSecond: 300
injectorLabel: vault-db-injector
defaultEngine: databases
```

#  1 <a name='Modetoken-renewer-1'></a>Mode token-renewer
The Revoker one is a process that is going to watch pod deletion Kubernetes events filtered with the label `vault-db-injector: true` and will revoke token attached to the pod when it is deleted 
The config file path can be parse by adding the path with : `- "--config=/revoker/config.yaml"`
```yaml
vaultAddress: https://vault1.tld:8200
vaultAuthPath: pgsql2-dv-kubernetes1-dv-par5
logLevel: info
kubeRole: all-rw
tokenTTL: 768h
vaultSecretName: vault-injector
vaultSecretPrefix: kubernetes1-dv-par5
mode: revoker
sentry: true
sentryDsn: https://my-sentry-url@sentry.tld/660
injectorLabel: vault-db-injector
defaultEngine: databases
```
