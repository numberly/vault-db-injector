package k8s

import (
	"strings"

	"github.com/cockroachdb/errors"
	"github.com/numberly/vault-db-injector/pkg/config"
	"github.com/numberly/vault-db-injector/pkg/logger"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	_ "k8s.io/client-go/plugin/pkg/client/auth/oidc"
)

const (
	ANNOTATION_VAULT_DB_PATH  string = "db-creds-injector.numberly.io/cluster"
	ANNOTATION_ROLE           string = "db-creds-injector.numberly.io/role"
	ANNOTATION_MODE           string = "db-creds-injector.numberly.io/mode" // DEFAULT_TO : classic. can be : classic, uri, file
	ANNOTATION_VAULT_POD_UUID string = "db-creds-injector.numberly.io/uuid"
	ANNOTATION_NRI_MAPPING    string = "db-creds-injector.numberly.io/nri-mapping"

	// DbMode constants for database credential injection mode.
	DbModeClassic = "classic"
	DbModeURI     = "uri"
)

// ErrV1AnnotationDetected is returned when a pod uses the deprecated vault injector v1 annotation (revoke-ttl).
var ErrV1AnnotationDetected = errors.New("this pod is going to be ignored, old annotation from vault injector v1 detected")

type ParserService struct {
	cfg config.Config
	log logger.Logger
	pod *corev1.Pod
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

type PodDbConfig struct {
	VaultDbPath      string
	Mode             string
	DbConfigurations *[]DbConfiguration
}

func NewParserService(cfg config.Config, pod *corev1.Pod) *ParserService {
	return &ParserService{
		cfg: cfg,
		pod: pod,
		log: logger.GetLogger(),
	}
}

func (s *ParserService) GetPodDbConfig(contextID string) (*PodDbConfig, error) {
	estimatedSize := len(s.pod.Annotations)
	dbConfigurations := make([]DbConfiguration, 0, estimatedSize)
	vaultDbPath, ok := s.pod.Annotations[ANNOTATION_VAULT_DB_PATH]
	if !ok || vaultDbPath == "" {
		vaultDbPath = s.cfg.DefaultEngine
	}

	for key, value := range s.pod.Annotations {
		if strings.HasPrefix(key, "db-creds-injector.numberly.io/revoke-ttl") {
			return nil, ErrV1AnnotationDetected
		}
		if strings.HasPrefix(key, "db-creds-injector.numberly.io/") {
			// Extract the database name and configuration type (e.g., dbname, dbaddress) from the key
			keyParts := strings.SplitN(key, "/", 2)
			if len(keyParts) < 2 {
				s.log.WithFields(logrus.Fields{"contextID": contextID}).Printf("Warning: Annotation '%s' does not follow the expected format 'db-creds-injector.numberly.io/dbname.configtype'", key)
				continue // Skip if the annotation doesn't follow the expected format
			}
			dbConfigKeyParts := strings.SplitN(keyParts[1], ".", 2)
			if len(dbConfigKeyParts) < 2 {
				s.log.WithFields(logrus.Fields{"contextID": contextID}).Printf("Warning: Configuration for '%s' does not include a database name and type", key)
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
				dbc.Role = s.pod.Annotations[ANNOTATION_ROLE]

			}

			s.log.WithFields(logrus.Fields{"contextID": contextID}).Infof("The role value is : %s", dbc.Role)

			// Assign the configuration value based on the type
			switch configType {
			case "env-key-uri":
				dbc.DbURIEnvKey = value
			case "template":
				dbc.Template = value
			case "mode":
				dbc.Mode = strings.ToLower(value)
			case "env-key-dbuser":
				dbc.DbUserEnvKey = value
			case "env-key-dbpassword":
				dbc.DbPasswordEnvKey = value
			case "role":
				dbc.Role = value
			default:
				s.log.WithFields(logrus.Fields{"contextID": contextID}).Infof("db configuration is not handled : %s", configType)
			}

		}
	}

	PodDbConfig := PodDbConfig{
		VaultDbPath:      vaultDbPath,
		DbConfigurations: &dbConfigurations,
	}

	return &PodDbConfig, nil
}
