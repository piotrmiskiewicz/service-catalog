## Overview

Service Catalog upgrade from version 0.2.x (and earlier) to 0.3.x needs a data migration. This document describes how the migration works and what action must be performed.

## Upgrade Service Catalog as a Helm release

The Service Catalog helm release can be upgraded using `helm upgrade` command, which runs all necessary actions.

### Details of an upgrade and migration

The upgrade to CRDs contains the following steps:
1. Make API Server read only. Before any backup we should block any resource changes to be sure the backup makes a snapshot. We need to avoid any changes while migration tool is backuping resources.
2. Scale down controller manager to avoid resources processing, for example secret deletion.
3. Backup all 8 type of SC resources to files in a Persistent Volume
4. Remove `OwnerReference` fields in all secrets pointed by any ServiceBinding. This is needed to avoid Secret deletion.
5. Remove all SC resources. This must be done if Service Catalog uses the main Kubernetes ETCD instance.
6. Upgrade Service Catalog: remove API Server, install CRDs, webhook and roll up the controller manager.
7. Scale down controller-manager to avoid any resource processing while applying resources.
8. Restore all resources. The migration tool sets all necessary fields added in Service Catalog 0.3.0. Creating resources triggers all logic implemented in webhooks so we can be sure all data are consistent.
Service instances are created and then updated because of class/plan refs fields. The validation webhooks denies creting service instances with non-empty fields: Spec.ClusterServiceClassRef, Spec.ClusterServicePlanRef, Spec.ServiceClassRef, Spec.ServicePlanRef.
These fields are set during an update operation.
9. Add proper owner reference to all secrets pointed by service bindings.
10. Scale up controller-manager. 

>Note: There is no difference between upgrade Service Catalog using own ETCD or main Kubernetes ETCD.
## Manual Service Catalog upgrade

### Backup

Execute `backup action` to scale down the controller, remove owner referneces in secrets and store all resources in a specified folder.

TODO: migration tool execution

### Clean up resources 

If the Service Catalog uses main Kubernetes ETCD, all Service Catalog resources must be deleted.
TODO: cleaner tool execution

### Upgrade

Uninstall old Service Catalog and install the new one (version 0.3.0).

### Restore

Execute `restore action` to restore all resources.

TODO: migration tool execution 

