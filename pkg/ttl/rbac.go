package ttl

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// OrphanedResource describes a resource that is orphaned and can be cleaned up.
type OrphanedResource struct {
	Kind      string
	Name      string
	Namespace string
}

func (o OrphanedResource) String() string {
	if o.Namespace != "" {
		return fmt.Sprintf("%s %s in namespace %s", o.Kind, o.Name, o.Namespace)
	}

	return fmt.Sprintf("%s %s (cluster-scoped)", o.Kind, o.Name)
}

func resourceLabels(releaseName, releaseNamespace string) map[string]string {
	return map[string]string{
		LabelManagedBy:        LabelManagedByValue,
		LabelRelease:          releaseName,
		LabelReleaseNamespace: releaseNamespace,
	}
}

// CreateServiceAccountAndRBAC creates the ServiceAccount and RBAC resources needed
// by the CronJob to uninstall a Helm release.
func CreateServiceAccountAndRBAC(ctx context.Context, client kubernetes.Interface, releaseName, releaseNamespace, cronjobNamespace, serviceAccountName string, deleteNamespace bool) error {
	if deleteNamespace && releaseNamespace == cronjobNamespace {
		return fmt.Errorf("cannot use --delete-namespace when CronJob namespace equals release namespace")
	}

	name, err := ResourceName(releaseName, releaseNamespace)
	if err != nil {
		return err
	}

	labels := resourceLabels(releaseName, releaseNamespace)

	// Create ServiceAccount in the CronJob namespace
	sa := &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceAccountName,
			Namespace: cronjobNamespace,
			Labels:    labels,
		},
	}

	if err := createOrUpdateServiceAccount(ctx, client, sa); err != nil {
		return fmt.Errorf("failed to create service account: %w", err)
	}

	if releaseNamespace == cronjobNamespace {
		return createSameNamespaceRBAC(ctx, client, name, serviceAccountName, releaseNamespace, labels)
	}

	if err := createCrossNamespaceRBAC(ctx, client, name, serviceAccountName, releaseNamespace, cronjobNamespace, labels); err != nil {
		return err
	}

	if deleteNamespace {
		return createDeleteNamespaceRBAC(ctx, client, name, serviceAccountName, cronjobNamespace, labels)
	}

	return nil
}

func createSameNamespaceRBAC(ctx context.Context, client kubernetes.Interface, name, serviceAccountName, namespace string, labels map[string]string) error {
	role := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    labels,
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{""},
				Resources: []string{"secrets"},
				Verbs:     []string{"get", "list", "delete"},
			},
			{
				APIGroups: []string{"batch"},
				Resources: []string{"cronjobs"},
				Verbs:     []string{"get", "delete"},
			},
		},
	}

	if err := createOrUpdateRole(ctx, client, role); err != nil {
		return fmt.Errorf("failed to create role: %w", err)
	}

	binding := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    labels,
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      serviceAccountName,
				Namespace: namespace,
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     name,
		},
	}

	if err := createOrUpdateRoleBinding(ctx, client, binding); err != nil {
		return fmt.Errorf("failed to create role binding: %w", err)
	}

	return nil
}

func createCrossNamespaceRBAC(ctx context.Context, client kubernetes.Interface, name, serviceAccountName, releaseNamespace, cronjobNamespace string, labels map[string]string) error {
	// Role in release namespace for secrets access
	releaseRole := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: releaseNamespace,
			Labels:    labels,
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{""},
				Resources: []string{"secrets"},
				Verbs:     []string{"get", "list", "delete"},
			},
		},
	}

	if err := createOrUpdateRole(ctx, client, releaseRole); err != nil {
		return fmt.Errorf("failed to create role in release namespace: %w", err)
	}

	// RoleBinding in release namespace
	releaseBinding := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: releaseNamespace,
			Labels:    labels,
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      serviceAccountName,
				Namespace: cronjobNamespace,
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     name,
		},
	}

	if err := createOrUpdateRoleBinding(ctx, client, releaseBinding); err != nil {
		return fmt.Errorf("failed to create role binding in release namespace: %w", err)
	}

	// Role in CronJob namespace for self-cleanup
	cronjobRole := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: cronjobNamespace,
			Labels:    labels,
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{"batch"},
				Resources: []string{"cronjobs"},
				Verbs:     []string{"get", "delete"},
			},
		},
	}

	if err := createOrUpdateRole(ctx, client, cronjobRole); err != nil {
		return fmt.Errorf("failed to create role in CronJob namespace: %w", err)
	}

	// RoleBinding in CronJob namespace
	cronjobBinding := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: cronjobNamespace,
			Labels:    labels,
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      serviceAccountName,
				Namespace: cronjobNamespace,
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     name,
		},
	}

	if err := createOrUpdateRoleBinding(ctx, client, cronjobBinding); err != nil {
		return fmt.Errorf("failed to create role binding in CronJob namespace: %w", err)
	}

	return nil
}

func createDeleteNamespaceRBAC(ctx context.Context, client kubernetes.Interface, name, serviceAccountName, cronjobNamespace string, labels map[string]string) error {
	clusterRole := &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: labels,
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{""},
				Resources: []string{"namespaces"},
				Verbs:     []string{"get", "delete"},
			},
		},
	}

	if err := createOrUpdateClusterRole(ctx, client, clusterRole); err != nil {
		return fmt.Errorf("failed to create cluster role: %w", err)
	}

	clusterBinding := &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: labels,
		},
		Subjects: []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      serviceAccountName,
				Namespace: cronjobNamespace,
			},
		},
		RoleRef: rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "ClusterRole",
			Name:     name,
		},
	}

	if err := createOrUpdateClusterRoleBinding(ctx, client, clusterBinding); err != nil {
		return fmt.Errorf("failed to create cluster role binding: %w", err)
	}

	return nil
}

// CleanupRBAC deletes all RBAC resources created for a specific release TTL.
func CleanupRBAC(ctx context.Context, client kubernetes.Interface, releaseName, releaseNamespace, cronjobNamespace string) error {
	name, err := ResourceName(releaseName, releaseNamespace)
	if err != nil {
		return err
	}

	// Delete ClusterRoleBinding (may not exist)
	err = client.RbacV1().ClusterRoleBindings().Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("failed to delete cluster role binding: %w", err)
	}

	// Delete ClusterRole (may not exist)
	err = client.RbacV1().ClusterRoles().Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("failed to delete cluster role: %w", err)
	}

	// Delete resources in release namespace
	if err := deleteNamespacedRBAC(ctx, client, name, releaseNamespace); err != nil {
		return err
	}

	// Delete resources in CronJob namespace (if different)
	if cronjobNamespace != releaseNamespace {
		if err := deleteNamespacedRBAC(ctx, client, name, cronjobNamespace); err != nil {
			return err
		}
	}

	// Delete ServiceAccount in CronJob namespace
	err = client.CoreV1().ServiceAccounts(cronjobNamespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("failed to delete service account: %w", err)
	}

	return nil
}

func deleteNamespacedRBAC(ctx context.Context, client kubernetes.Interface, name, namespace string) error {
	err := client.RbacV1().RoleBindings(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("failed to delete role binding in namespace %s: %w", namespace, err)
	}

	err = client.RbacV1().Roles(namespace).Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("failed to delete role in namespace %s: %w", namespace, err)
	}

	return nil
}

// CleanupOrphaned finds and optionally deletes orphaned RBAC resources whose
// CronJobs no longer exist.
func CleanupOrphaned(ctx context.Context, client kubernetes.Interface, namespaces []string, allNamespaces bool, dryRun bool) ([]OrphanedResource, error) {
	labelSelector := fmt.Sprintf("%s=%s", LabelManagedBy, LabelManagedByValue)
	var orphaned []OrphanedResource

	if allNamespaces {
		nsList, err := client.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, fmt.Errorf("failed to list namespaces: %w", err)
		}

		namespaces = make([]string, 0, len(nsList.Items))
		for _, ns := range nsList.Items {
			namespaces = append(namespaces, ns.Name)
		}
	}

	// Check cluster-scoped resources first
	clusterBindings, err := client.RbacV1().ClusterRoleBindings().List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list cluster role bindings: %w", err)
	}

	for _, crb := range clusterBindings.Items {
		if isOrphaned(ctx, client, crb.Labels) {
			orphaned = append(orphaned, OrphanedResource{Kind: "ClusterRoleBinding", Name: crb.Name})
			if !dryRun {
				if err := client.RbacV1().ClusterRoleBindings().Delete(ctx, crb.Name, metav1.DeleteOptions{}); err != nil && !errors.IsNotFound(err) {
					return orphaned, fmt.Errorf("failed to delete cluster role binding %s: %w", crb.Name, err)
				}
			}
		}
	}

	clusterRoles, err := client.RbacV1().ClusterRoles().List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list cluster roles: %w", err)
	}

	for _, cr := range clusterRoles.Items {
		if isOrphaned(ctx, client, cr.Labels) {
			orphaned = append(orphaned, OrphanedResource{Kind: "ClusterRole", Name: cr.Name})
			if !dryRun {
				if err := client.RbacV1().ClusterRoles().Delete(ctx, cr.Name, metav1.DeleteOptions{}); err != nil && !errors.IsNotFound(err) {
					return orphaned, fmt.Errorf("failed to delete cluster role %s: %w", cr.Name, err)
				}
			}
		}
	}

	// Check namespaced resources
	for _, ns := range namespaces {
		bindings, err := client.RbacV1().RoleBindings(ns).List(ctx, metav1.ListOptions{
			LabelSelector: labelSelector,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to list role bindings in %s: %w", ns, err)
		}

		for _, rb := range bindings.Items {
			if isOrphaned(ctx, client, rb.Labels) {
				orphaned = append(orphaned, OrphanedResource{Kind: "RoleBinding", Name: rb.Name, Namespace: ns})
				if !dryRun {
					if err := client.RbacV1().RoleBindings(ns).Delete(ctx, rb.Name, metav1.DeleteOptions{}); err != nil && !errors.IsNotFound(err) {
						return orphaned, fmt.Errorf("failed to delete role binding %s in %s: %w", rb.Name, ns, err)
					}
				}
			}
		}

		roles, err := client.RbacV1().Roles(ns).List(ctx, metav1.ListOptions{
			LabelSelector: labelSelector,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to list roles in %s: %w", ns, err)
		}

		for _, role := range roles.Items {
			if isOrphaned(ctx, client, role.Labels) {
				orphaned = append(orphaned, OrphanedResource{Kind: "Role", Name: role.Name, Namespace: ns})
				if !dryRun {
					if err := client.RbacV1().Roles(ns).Delete(ctx, role.Name, metav1.DeleteOptions{}); err != nil && !errors.IsNotFound(err) {
						return orphaned, fmt.Errorf("failed to delete role %s in %s: %w", role.Name, ns, err)
					}
				}
			}
		}

		sas, err := client.CoreV1().ServiceAccounts(ns).List(ctx, metav1.ListOptions{
			LabelSelector: labelSelector,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to list service accounts in %s: %w", ns, err)
		}

		for _, sa := range sas.Items {
			if isOrphaned(ctx, client, sa.Labels) {
				orphaned = append(orphaned, OrphanedResource{Kind: "ServiceAccount", Name: sa.Name, Namespace: ns})
				if !dryRun {
					if err := client.CoreV1().ServiceAccounts(ns).Delete(ctx, sa.Name, metav1.DeleteOptions{}); err != nil && !errors.IsNotFound(err) {
						return orphaned, fmt.Errorf("failed to delete service account %s in %s: %w", sa.Name, ns, err)
					}
				}
			}
		}
	}

	return orphaned, nil
}

// isOrphaned checks if the CronJob for a release still exists.
func isOrphaned(ctx context.Context, client kubernetes.Interface, labels map[string]string) bool {
	releaseName := labels[LabelRelease]
	releaseNs := labels[LabelReleaseNamespace]
	cronjobNs := labels[LabelCronjobNamespace]
	if cronjobNs == "" {
		cronjobNs = releaseNs
	}

	name, err := ResourceName(releaseName, releaseNs)
	if err != nil {
		return false
	}

	_, err = client.BatchV1().CronJobs(cronjobNs).Get(ctx, name, metav1.GetOptions{})
	return errors.IsNotFound(err)
}

// createOrUpdate helpers that are idempotent

func createOrUpdateServiceAccount(ctx context.Context, client kubernetes.Interface, sa *corev1.ServiceAccount) error {
	_, err := client.CoreV1().ServiceAccounts(sa.Namespace).Create(ctx, sa, metav1.CreateOptions{})
	if errors.IsAlreadyExists(err) {
		existing, getErr := client.CoreV1().ServiceAccounts(sa.Namespace).Get(ctx, sa.Name, metav1.GetOptions{})
		if getErr != nil {
			return getErr
		}

		existing.Labels = sa.Labels
		_, err = client.CoreV1().ServiceAccounts(sa.Namespace).Update(ctx, existing, metav1.UpdateOptions{})
	}

	return err
}

func createOrUpdateRole(ctx context.Context, client kubernetes.Interface, role *rbacv1.Role) error {
	_, err := client.RbacV1().Roles(role.Namespace).Create(ctx, role, metav1.CreateOptions{})
	if errors.IsAlreadyExists(err) {
		existing, getErr := client.RbacV1().Roles(role.Namespace).Get(ctx, role.Name, metav1.GetOptions{})
		if getErr != nil {
			return getErr
		}

		existing.Labels = role.Labels
		existing.Rules = role.Rules
		_, err = client.RbacV1().Roles(role.Namespace).Update(ctx, existing, metav1.UpdateOptions{})
	}

	return err
}

func createOrUpdateRoleBinding(ctx context.Context, client kubernetes.Interface, binding *rbacv1.RoleBinding) error {
	_, err := client.RbacV1().RoleBindings(binding.Namespace).Create(ctx, binding, metav1.CreateOptions{})
	if errors.IsAlreadyExists(err) {
		existing, getErr := client.RbacV1().RoleBindings(binding.Namespace).Get(ctx, binding.Name, metav1.GetOptions{})
		if getErr != nil {
			return getErr
		}

		existing.Labels = binding.Labels
		existing.Subjects = binding.Subjects
		existing.RoleRef = binding.RoleRef
		_, err = client.RbacV1().RoleBindings(binding.Namespace).Update(ctx, existing, metav1.UpdateOptions{})
	}

	return err
}

func createOrUpdateClusterRole(ctx context.Context, client kubernetes.Interface, role *rbacv1.ClusterRole) error {
	_, err := client.RbacV1().ClusterRoles().Create(ctx, role, metav1.CreateOptions{})
	if errors.IsAlreadyExists(err) {
		existing, getErr := client.RbacV1().ClusterRoles().Get(ctx, role.Name, metav1.GetOptions{})
		if getErr != nil {
			return getErr
		}

		existing.Labels = role.Labels
		existing.Rules = role.Rules
		_, err = client.RbacV1().ClusterRoles().Update(ctx, existing, metav1.UpdateOptions{})
	}

	return err
}

func createOrUpdateClusterRoleBinding(ctx context.Context, client kubernetes.Interface, binding *rbacv1.ClusterRoleBinding) error {
	_, err := client.RbacV1().ClusterRoleBindings().Create(ctx, binding, metav1.CreateOptions{})
	if errors.IsAlreadyExists(err) {
		existing, getErr := client.RbacV1().ClusterRoleBindings().Get(ctx, binding.Name, metav1.GetOptions{})
		if getErr != nil {
			return getErr
		}

		existing.Labels = binding.Labels
		existing.Subjects = binding.Subjects
		existing.RoleRef = binding.RoleRef
		_, err = client.RbacV1().ClusterRoleBindings().Update(ctx, existing, metav1.UpdateOptions{})
	}

	return err
}
