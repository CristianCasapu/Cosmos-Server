package docker

// Fork addition (plan 45): what src/appsignin needs of this package's rules.

import (
	"regexp"

	doctype "github.com/docker/docker/api/types"
)

// IsSecretName tells whether a variable's name says it holds a secret, by the
// rule the start check masks with (PASSWORD, PASSPHRASE, PASSWD, SECRET,
// TOKEN, KEY, PRIVATE, CREDENTIAL) and PWD.
func IsSecretName(name string) bool {
	return secretName.MatchString(name) || pwdName.MatchString(name)
}

// ForkAdminLabel is a label of a container as the administrator set it: ""
// when it is absent or is the image's own (fork_image_labels.go).
func ForkAdminLabel(container doctype.ContainerJSON, key string) string {
	return adminLabel(container, key)
}

var pwdName = regexp.MustCompile(`(?i)(^|_)PWD$`)
