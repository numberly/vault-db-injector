package k8s

import (
	"errors"
	"strings"

	"github.com/numberly/vault-db-injector/pkg/config"
	"github.com/numberly/vault-db-injector/pkg/logger"
	corev1 "k8s.io/api/core/v1"
	_ "k8s.io/client-go/plugin/pkg/client/auth/oidc"
)

const (
	ANNOTATION_VAULT_DB_PATH  string = "db-creds-injector.numberly.io/cluster"
	ANNOTATION_ROLE           string = "db-creds-injector.numberly.io/role"
	ANNOTATION_MODE           string = "db-creds-injector.numberly.io/mode" // DEFAULT_TO : classic. can be : classic, uri, file
	ANNOTATION_VAULT_POD_UUID string = "db-creds-injector.numberly.io/uuid"
)

type ParserService struct {
	cfg config.Config
	log logger.Logger
	pod *corev1.Pod
}

type Parser interface {
	GetPodDbConfig() (*podDbConfig, error)
}

type DbConfiguration struct {
	DbURIEnvKey      string
	DbUserEnvKey     string
	DbPasswordEnvKey string
	DbName           string
	Mode             string
	Template         string
	Role             string
}

type podDbConfig struct {
	VaultDbPath      string
	Mode             string
	DbConfigurations *[]DbConfiguration
}

func NewService(cfg config.Config, pod *corev1.Pod) *ParserService {
	return &ParserService{
		cfg: cfg,
		pod: pod,
		log: logger.GetLogger(),
	}
}

func (s *ParserService) GetPodDbConfig() (*podDbConfig, error) {
	estimatedSize := len(s.pod.Annotations)
	dbConfigurations := make([]DbConfiguration, 0, estimatedSize)
	vaultDbPath, ok := s.pod.Annotations[ANNOTATION_VAULT_DB_PATH]
	if !ok || vaultDbPath == "" {
		vaultDbPath = s.cfg.DefaultEngine
	}

	for key, value := range s.pod.Annotations {
		if strings.HasPrefix(key, "db-creds-injector.numberly.io/revoke-ttl") {
			return nil, errors.New("this pod is going to be ignored, old annotation from vault injector v1 detected")
		}
		if strings.HasPrefix(key, "db-creds-injector.numberly.io/") {
			// Extract the database name and configuration type (e.g., dbname, dbaddress) from the key
			keyParts := strings.SplitN(key, "/", 2)
			if (len(keyParts) < 2 && key != "role") || (len(keyParts) < 2 && key != "cluster") {
				s.log.Printf("Warning: Annotation '%s' does not follow the expected format 'db-creds-injector.numberly.io/dbname.configtype'", key)
				continue // Skip if the annotation doesn't follow the expected format
			}
			dbConfigKeyParts := strings.SplitN(keyParts[1], ".", 2)
			if (len(dbConfigKeyParts) < 2 && key != "role") || (len(dbConfigKeyParts) < 2 && key != "cluster") {
				s.log.Printf("Warning: Configuration for '%s' does not include a database name and type", key)
				continue // Skip if the configuvaultConnation doesn't include a database name and type
			}
			dbName := dbConfigKeyParts[0]
			configType := dbConfigKeyParts[1]

			// Find or create the dbConfiguration for the dbName
			var dbc *DbConfiguration
			for i, dbConf := range dbConfigurations {
				if dbConf.DbName == dbName {
					dbc = &dbConfigurations[i]
					break
				}
			}
			if dbc == nil {
				// Create new dbConfiguration if one doesn't exist for the dbName
				newDbc := DbConfiguration{DbName: dbName}
				dbConfigurations = append(dbConfigurations, newDbc)
				dbc = &dbConfigurations[len(dbConfigurations)-1]
				dbc.Role, ok = s.pod.Annotations[ANNOTATION_ROLE]
				if !ok || dbc.Role == "" {
					dbc.Role = ""
				}

			}

			s.log.Infof("La valeur du role est : %s", dbc.Role)

			// Assign the configuration value based on the type
			switch configType {
			case "env-key-uri":
				dbc.DbURIEnvKey = value
			case "template":
				dbc.Template = value
			case "mode":
				dbc.Mode = value
			case "env-key-dbuser":
				dbc.DbUserEnvKey = value
			case "env-key-dbpassword":
				dbc.DbPasswordEnvKey = value
			case "role":
				dbc.Role = value
			default:
				s.log.Infof("db configuration is not handled : %s", configType)
			}

		}
	}

	podDbConfig := podDbConfig{
		VaultDbPath:      vaultDbPath,
		DbConfigurations: &dbConfigurations,
	}

	return &podDbConfig, nil
}
