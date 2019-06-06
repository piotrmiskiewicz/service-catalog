/*
Copyright 2019 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package migration

import (
	"fmt"
	"github.com/spf13/pflag"
)

const (
	removeCRD = "remove-crd"

	webhookConfigurationsNames       = "webhook-configurations"
	serviceCatalogNamespaceParameter = "service-catalog-namespace"
	controllerManagerNameParameter   = "controller-manager-deployment"
	storagePathParameter             = "storage-path"

	backupActionName  = "backup"
	restoreActionName = "restore"
)

// MigrationOptions holds configuration for the migration job
type MigrationOptions struct {
	Action                string
	StoragePath           string
	ReleaseNamespace      string
	ControllerManagerName string
}

// NewMigrationOptions creates and returns a new MigrationOptions
func NewMigrationOptions() *MigrationOptions {
	return &MigrationOptions{}
}

// AddFlags adds flags for a CleanerOptions to the specified FlagSet.
func (c *MigrationOptions) AddFlags(fs *pflag.FlagSet) {
	fs.StringVar(&c.Action, "action", "", "Command name to execute")
	fs.StringVar(&c.StoragePath, storagePathParameter, "", "Path to a directory, where all Service Catalog resources must be saved")
	fs.StringVar(&c.ReleaseNamespace, serviceCatalogNamespaceParameter, "", "Name of namespace where Service Catalog is released")
	fs.StringVar(&c.ControllerManagerName, controllerManagerNameParameter, "", "Name of controller manager deployment")
}

// Validate checks flag has been set and has a proper value
func (c *MigrationOptions) Validate() error {
	switch c.Action {
	case backupActionName:
	case restoreActionName:
	default:
		return fmt.Errorf("action msut be 'restore' or 'backup'")
	}
	if c.StoragePath == "" {
		return fmt.Errorf("%s must not be empty", storagePathParameter)
	}
	if c.ReleaseNamespace == "" {
		return fmt.Errorf("%s must not be empty", serviceCatalogNamespaceParameter)
	}
	if c.ControllerManagerName == "" {
		return fmt.Errorf("%s must not be empty", controllerManagerNameParameter)
	}
	return nil
}
