package ttl

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestCreateServiceAccountAndRBAC_SameNamespace(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()

	err := CreateServiceAccountAndRBAC(ctx, client, "myapp", "default", "default", "myapp-default-ttl", false)
	require.NoError(t, err)

	// Verify SA created
	sa, err := client.CoreV1().ServiceAccounts("default").Get(ctx, "myapp-default-ttl", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, LabelManagedByValue, sa.Labels[LabelManagedBy])
	assert.Equal(t, "myapp", sa.Labels[LabelRelease])

	// Verify Role
	role, err := client.RbacV1().Roles("default").Get(ctx, "myapp-default-ttl", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Len(t, role.Rules, 2)
	assert.Equal(t, []string{"secrets"}, role.Rules[0].Resources)
	assert.Equal(t, []string{"cronjobs"}, role.Rules[1].Resources)

	// Verify RoleBinding
	binding, err := client.RbacV1().RoleBindings("default").Get(ctx, "myapp-default-ttl", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, "myapp-default-ttl", binding.Subjects[0].Name)
	assert.Equal(t, "default", binding.Subjects[0].Namespace)
}

func TestCreateServiceAccountAndRBAC_CrossNamespace(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()

	err := CreateServiceAccountAndRBAC(ctx, client, "myapp", "staging", "ops", "myapp-staging-ttl", false)
	require.NoError(t, err)

	// SA in CronJob namespace
	sa, err := client.CoreV1().ServiceAccounts("ops").Get(ctx, "myapp-staging-ttl", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, LabelManagedByValue, sa.Labels[LabelManagedBy])

	// Role in release namespace (secrets)
	releaseRole, err := client.RbacV1().Roles("staging").Get(ctx, "myapp-staging-ttl", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Len(t, releaseRole.Rules, 1)
	assert.Equal(t, []string{"secrets"}, releaseRole.Rules[0].Resources)

	// Role in CronJob namespace (cronjobs)
	cronjobRole, err := client.RbacV1().Roles("ops").Get(ctx, "myapp-staging-ttl", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Len(t, cronjobRole.Rules, 1)
	assert.Equal(t, []string{"cronjobs"}, cronjobRole.Rules[0].Resources)

	// RoleBinding in release namespace
	releaseBinding, err := client.RbacV1().RoleBindings("staging").Get(ctx, "myapp-staging-ttl", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, "ops", releaseBinding.Subjects[0].Namespace)

	// RoleBinding in CronJob namespace
	cronjobBinding, err := client.RbacV1().RoleBindings("ops").Get(ctx, "myapp-staging-ttl", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, "ops", cronjobBinding.Subjects[0].Namespace)

	// No ClusterRole or ClusterRoleBinding
	_, err = client.RbacV1().ClusterRoles().Get(ctx, "myapp-staging-ttl", metav1.GetOptions{})
	assert.Error(t, err)
}

func TestCreateServiceAccountAndRBAC_CrossNamespaceWithDeleteNamespace(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()

	err := CreateServiceAccountAndRBAC(ctx, client, "myapp", "staging", "ops", "myapp-staging-ttl", true)
	require.NoError(t, err)

	// All cross-namespace resources
	_, err = client.CoreV1().ServiceAccounts("ops").Get(ctx, "myapp-staging-ttl", metav1.GetOptions{})
	require.NoError(t, err)

	_, err = client.RbacV1().Roles("staging").Get(ctx, "myapp-staging-ttl", metav1.GetOptions{})
	require.NoError(t, err)

	_, err = client.RbacV1().Roles("ops").Get(ctx, "myapp-staging-ttl", metav1.GetOptions{})
	require.NoError(t, err)

	// Plus ClusterRole and ClusterRoleBinding
	cr, err := client.RbacV1().ClusterRoles().Get(ctx, "myapp-staging-ttl", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, []string{"namespaces"}, cr.Rules[0].Resources)
	assert.Equal(t, []string{"get", "delete"}, cr.Rules[0].Verbs)

	crb, err := client.RbacV1().ClusterRoleBindings().Get(ctx, "myapp-staging-ttl", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, "myapp-staging-ttl", crb.Subjects[0].Name)
	assert.Equal(t, "ops", crb.Subjects[0].Namespace)
}

func TestCreateServiceAccountAndRBAC_RejectsDeleteNamespaceSameNs(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()

	err := CreateServiceAccountAndRBAC(ctx, client, "myapp", "default", "default", "myapp-default-ttl", true)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cannot use --delete-namespace")
}

func TestCreateServiceAccountAndRBAC_Idempotent(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()

	// Create twice, should not error
	err := CreateServiceAccountAndRBAC(ctx, client, "myapp", "default", "default", "myapp-default-ttl", false)
	require.NoError(t, err)

	err = CreateServiceAccountAndRBAC(ctx, client, "myapp", "default", "default", "myapp-default-ttl", false)
	require.NoError(t, err)
}

func TestCleanupRBAC(t *testing.T) {
	ctx := context.Background()

	t.Run("cleans up same-namespace resources", func(t *testing.T) {
		client := fake.NewClientset()

		// Create resources first
		err := CreateServiceAccountAndRBAC(ctx, client, "myapp", "default", "default", "myapp-default-ttl", false)
		require.NoError(t, err)

		// Clean up
		err = CleanupRBAC(ctx, client, "myapp", "default", "default")
		require.NoError(t, err)

		// Verify all gone
		_, err = client.CoreV1().ServiceAccounts("default").Get(ctx, "myapp-default-ttl", metav1.GetOptions{})
		assert.Error(t, err)

		_, err = client.RbacV1().Roles("default").Get(ctx, "myapp-default-ttl", metav1.GetOptions{})
		assert.Error(t, err)

		_, err = client.RbacV1().RoleBindings("default").Get(ctx, "myapp-default-ttl", metav1.GetOptions{})
		assert.Error(t, err)
	})

	t.Run("cleans up cross-namespace resources with delete-namespace", func(t *testing.T) {
		client := fake.NewClientset()

		// Create all resources
		err := CreateServiceAccountAndRBAC(ctx, client, "myapp", "staging", "ops", "myapp-staging-ttl", true)
		require.NoError(t, err)

		// Clean up
		err = CleanupRBAC(ctx, client, "myapp", "staging", "ops")
		require.NoError(t, err)

		// Verify all gone
		_, err = client.CoreV1().ServiceAccounts("ops").Get(ctx, "myapp-staging-ttl", metav1.GetOptions{})
		assert.Error(t, err)

		_, err = client.RbacV1().ClusterRoles().Get(ctx, "myapp-staging-ttl", metav1.GetOptions{})
		assert.Error(t, err)

		_, err = client.RbacV1().ClusterRoleBindings().Get(ctx, "myapp-staging-ttl", metav1.GetOptions{})
		assert.Error(t, err)
	})

	t.Run("handles not-found gracefully", func(t *testing.T) {
		client := fake.NewClientset()

		// Clean up non-existent resources - should not error
		err := CleanupRBAC(ctx, client, "myapp", "default", "default")
		require.NoError(t, err)
	})
}

func TestCleanupOrphaned(t *testing.T) {
	ctx := context.Background()

	t.Run("finds orphaned resources - dry run", func(t *testing.T) {
		client := fake.NewClientset()

		// Create RBAC resources (simulating what CreateServiceAccountAndRBAC does)
		labels := map[string]string{
			LabelManagedBy:        LabelManagedByValue,
			LabelRelease:          "myapp",
			LabelReleaseNamespace: "default",
			LabelCronjobNamespace: "default",
		}

		_, err := client.CoreV1().ServiceAccounts("default").Create(ctx, &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{Name: "myapp-default-ttl", Namespace: "default", Labels: labels},
		}, metav1.CreateOptions{})
		require.NoError(t, err)

		_, err = client.RbacV1().Roles("default").Create(ctx, &rbacv1.Role{
			ObjectMeta: metav1.ObjectMeta{Name: "myapp-default-ttl", Namespace: "default", Labels: labels},
		}, metav1.CreateOptions{})
		require.NoError(t, err)

		_, err = client.RbacV1().RoleBindings("default").Create(ctx, &rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{Name: "myapp-default-ttl", Namespace: "default", Labels: labels},
			RoleRef:    rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "Role", Name: "myapp-default-ttl"},
		}, metav1.CreateOptions{})
		require.NoError(t, err)

		// No CronJob exists, so all resources are orphaned
		orphaned, err := CleanupOrphaned(ctx, client, []string{"default"}, false, true)
		require.NoError(t, err)
		assert.Len(t, orphaned, 3)

		// Verify resources still exist (dry run)
		_, err = client.CoreV1().ServiceAccounts("default").Get(ctx, "myapp-default-ttl", metav1.GetOptions{})
		require.NoError(t, err)
	})

	t.Run("deletes orphaned resources - no dry run", func(t *testing.T) {
		client := fake.NewClientset()

		labels := map[string]string{
			LabelManagedBy:        LabelManagedByValue,
			LabelRelease:          "myapp",
			LabelReleaseNamespace: "default",
			LabelCronjobNamespace: "default",
		}

		_, err := client.CoreV1().ServiceAccounts("default").Create(ctx, &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{Name: "myapp-default-ttl", Namespace: "default", Labels: labels},
		}, metav1.CreateOptions{})
		require.NoError(t, err)

		_, err = client.RbacV1().Roles("default").Create(ctx, &rbacv1.Role{
			ObjectMeta: metav1.ObjectMeta{Name: "myapp-default-ttl", Namespace: "default", Labels: labels},
		}, metav1.CreateOptions{})
		require.NoError(t, err)

		orphaned, err := CleanupOrphaned(ctx, client, []string{"default"}, false, false)
		require.NoError(t, err)
		assert.NotEmpty(t, orphaned)

		// Verify resources deleted
		_, err = client.CoreV1().ServiceAccounts("default").Get(ctx, "myapp-default-ttl", metav1.GetOptions{})
		assert.Error(t, err)
	})

	t.Run("skips non-orphaned resources", func(t *testing.T) {
		client := fake.NewClientset()

		labels := map[string]string{
			LabelManagedBy:        LabelManagedByValue,
			LabelRelease:          "myapp",
			LabelReleaseNamespace: "default",
			LabelCronjobNamespace: "default",
		}

		// Create both RBAC and CronJob (not orphaned)
		_, err := client.CoreV1().ServiceAccounts("default").Create(ctx, &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{Name: "myapp-default-ttl", Namespace: "default", Labels: labels},
		}, metav1.CreateOptions{})
		require.NoError(t, err)

		_, err = client.BatchV1().CronJobs("default").Create(ctx, &batchv1.CronJob{
			ObjectMeta: metav1.ObjectMeta{Name: "myapp-default-ttl", Namespace: "default", Labels: labels},
		}, metav1.CreateOptions{})
		require.NoError(t, err)

		orphaned, err := CleanupOrphaned(ctx, client, []string{"default"}, false, true)
		require.NoError(t, err)
		assert.Empty(t, orphaned)
	})

	t.Run("handles cluster-scoped orphans", func(t *testing.T) {
		client := fake.NewClientset()

		labels := map[string]string{
			LabelManagedBy:        LabelManagedByValue,
			LabelRelease:          "myapp",
			LabelReleaseNamespace: "staging",
			LabelCronjobNamespace: "ops",
		}

		_, err := client.RbacV1().ClusterRoles().Create(ctx, &rbacv1.ClusterRole{
			ObjectMeta: metav1.ObjectMeta{Name: "myapp-staging-ttl", Labels: labels},
		}, metav1.CreateOptions{})
		require.NoError(t, err)

		_, err = client.RbacV1().ClusterRoleBindings().Create(ctx, &rbacv1.ClusterRoleBinding{
			ObjectMeta: metav1.ObjectMeta{Name: "myapp-staging-ttl", Labels: labels},
			RoleRef:    rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "ClusterRole", Name: "myapp-staging-ttl"},
		}, metav1.CreateOptions{})
		require.NoError(t, err)

		orphaned, err := CleanupOrphaned(ctx, client, []string{}, false, true)
		require.NoError(t, err)
		assert.Len(t, orphaned, 2)

		kinds := make([]string, 0, len(orphaned))
		for _, o := range orphaned {
			kinds = append(kinds, o.Kind)
		}
		assert.Contains(t, kinds, "ClusterRole")
		assert.Contains(t, kinds, "ClusterRoleBinding")
	})
}

func TestOrphanedResource_String(t *testing.T) {
	t.Run("namespaced resource", func(t *testing.T) {
		o := OrphanedResource{Kind: "ServiceAccount", Name: "myapp-staging-ttl", Namespace: "ops"}
		assert.Equal(t, "ServiceAccount myapp-staging-ttl in namespace ops", o.String())
	})

	t.Run("cluster-scoped resource", func(t *testing.T) {
		o := OrphanedResource{Kind: "ClusterRole", Name: "myapp-staging-ttl"}
		assert.Equal(t, "ClusterRole myapp-staging-ttl (cluster-scoped)", o.String())
	})
}

func TestCreateServiceAccountAndRBAC_IdempotentCrossNamespace(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()

	// Create cross-namespace with delete-namespace, twice
	err := CreateServiceAccountAndRBAC(ctx, client, "myapp", "staging", "ops", "myapp-staging-ttl", true)
	require.NoError(t, err)

	err = CreateServiceAccountAndRBAC(ctx, client, "myapp", "staging", "ops", "myapp-staging-ttl", true)
	require.NoError(t, err)

	// Verify resources still exist and are correct
	cr, err := client.RbacV1().ClusterRoles().Get(ctx, "myapp-staging-ttl", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, []string{"namespaces"}, cr.Rules[0].Resources)

	crb, err := client.RbacV1().ClusterRoleBindings().Get(ctx, "myapp-staging-ttl", metav1.GetOptions{})
	require.NoError(t, err)
	assert.Equal(t, "myapp-staging-ttl", crb.Subjects[0].Name)
}

func TestCleanupOrphaned_AllNamespaces(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()

	labels := map[string]string{
		LabelManagedBy:        LabelManagedByValue,
		LabelRelease:          "myapp",
		LabelReleaseNamespace: "staging",
		LabelCronjobNamespace: "ops",
	}

	// Create namespace objects for allNamespaces discovery
	_, err := client.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "ops"},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	_, err = client.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "staging"},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	// Create orphaned SA in ops namespace
	_, err = client.CoreV1().ServiceAccounts("ops").Create(ctx, &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{Name: "myapp-staging-ttl", Namespace: "ops", Labels: labels},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	// Create orphaned role in staging
	_, err = client.RbacV1().Roles("staging").Create(ctx, &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{Name: "myapp-staging-ttl", Namespace: "staging", Labels: labels},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	orphaned, err := CleanupOrphaned(ctx, client, nil, true, true)
	require.NoError(t, err)
	assert.NotEmpty(t, orphaned)
}

func TestCleanupOrphaned_DeletesClusterScopedOrphans(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()

	labels := map[string]string{
		LabelManagedBy:        LabelManagedByValue,
		LabelRelease:          "myapp",
		LabelReleaseNamespace: "staging",
		LabelCronjobNamespace: "ops",
	}

	_, err := client.RbacV1().ClusterRoles().Create(ctx, &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{Name: "myapp-staging-ttl", Labels: labels},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	_, err = client.RbacV1().ClusterRoleBindings().Create(ctx, &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "myapp-staging-ttl", Labels: labels},
		RoleRef:    rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "ClusterRole", Name: "myapp-staging-ttl"},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	// Delete (not dry-run)
	orphaned, err := CleanupOrphaned(ctx, client, []string{}, false, false)
	require.NoError(t, err)
	assert.Len(t, orphaned, 2)

	// Verify deleted
	_, err = client.RbacV1().ClusterRoles().Get(ctx, "myapp-staging-ttl", metav1.GetOptions{})
	assert.Error(t, err)

	_, err = client.RbacV1().ClusterRoleBindings().Get(ctx, "myapp-staging-ttl", metav1.GetOptions{})
	assert.Error(t, err)
}

func TestCleanupOrphaned_DeletesNamespacedOrphans(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()

	labels := map[string]string{
		LabelManagedBy:        LabelManagedByValue,
		LabelRelease:          "myapp",
		LabelReleaseNamespace: "default",
		LabelCronjobNamespace: "default",
	}

	_, err := client.RbacV1().RoleBindings("default").Create(ctx, &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "myapp-default-ttl", Namespace: "default", Labels: labels},
		RoleRef:    rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "Role", Name: "myapp-default-ttl"},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	_, err = client.RbacV1().Roles("default").Create(ctx, &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{Name: "myapp-default-ttl", Namespace: "default", Labels: labels},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	_, err = client.CoreV1().ServiceAccounts("default").Create(ctx, &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{Name: "myapp-default-ttl", Namespace: "default", Labels: labels},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	// Delete (not dry-run)
	orphaned, err := CleanupOrphaned(ctx, client, []string{"default"}, false, false)
	require.NoError(t, err)
	assert.Len(t, orphaned, 3)

	// Verify all deleted
	_, err = client.RbacV1().RoleBindings("default").Get(ctx, "myapp-default-ttl", metav1.GetOptions{})
	assert.Error(t, err)

	_, err = client.RbacV1().Roles("default").Get(ctx, "myapp-default-ttl", metav1.GetOptions{})
	assert.Error(t, err)

	_, err = client.CoreV1().ServiceAccounts("default").Get(ctx, "myapp-default-ttl", metav1.GetOptions{})
	assert.Error(t, err)
}

// Reactor-based error tests to cover error paths in RBAC functions

func TestCreateServiceAccountAndRBAC_SACreateError(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	client.PrependReactor("create", "serviceaccounts", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("simulated SA create error")
	})

	err := CreateServiceAccountAndRBAC(ctx, client, "myapp", "default", "default", "myapp-default-ttl", false)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create service account")
}

func TestCreateServiceAccountAndRBAC_RoleCreateError(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	client.PrependReactor("create", "roles", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("simulated role create error")
	})

	err := CreateServiceAccountAndRBAC(ctx, client, "myapp", "default", "default", "myapp-default-ttl", false)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create role")
}

func TestCreateServiceAccountAndRBAC_RoleBindingCreateError(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	client.PrependReactor("create", "rolebindings", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("simulated rolebinding create error")
	})

	err := CreateServiceAccountAndRBAC(ctx, client, "myapp", "default", "default", "myapp-default-ttl", false)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create role binding")
}

func TestCreateServiceAccountAndRBAC_CrossNS_ReleaseRoleError(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	client.PrependReactor("create", "roles", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("simulated role error")
	})

	err := CreateServiceAccountAndRBAC(ctx, client, "myapp", "staging", "ops", "myapp-staging-ttl", false)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create role in release namespace")
}

func TestCreateServiceAccountAndRBAC_CrossNS_ReleaseBindingError(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	client.PrependReactor("create", "rolebindings", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("simulated binding error")
	})

	err := CreateServiceAccountAndRBAC(ctx, client, "myapp", "staging", "ops", "myapp-staging-ttl", false)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create role binding in release namespace")
}

func TestCreateServiceAccountAndRBAC_CrossNS_CronjobRoleError(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	callCount := 0
	client.PrependReactor("create", "roles", func(action k8stesting.Action) (bool, runtime.Object, error) {
		callCount++
		if callCount == 2 {
			return true, nil, fmt.Errorf("simulated cronjob role error")
		}
		return false, nil, nil
	})

	err := CreateServiceAccountAndRBAC(ctx, client, "myapp", "staging", "ops", "myapp-staging-ttl", false)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create role in CronJob namespace")
}

func TestCreateServiceAccountAndRBAC_CrossNS_CronjobBindingError(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	callCount := 0
	client.PrependReactor("create", "rolebindings", func(action k8stesting.Action) (bool, runtime.Object, error) {
		callCount++
		if callCount == 2 {
			return true, nil, fmt.Errorf("simulated cronjob binding error")
		}
		return false, nil, nil
	})

	err := CreateServiceAccountAndRBAC(ctx, client, "myapp", "staging", "ops", "myapp-staging-ttl", false)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create role binding in CronJob namespace")
}

func TestCreateServiceAccountAndRBAC_DeleteNS_ClusterRoleError(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	client.PrependReactor("create", "clusterroles", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("simulated cluster role error")
	})

	err := CreateServiceAccountAndRBAC(ctx, client, "myapp", "staging", "ops", "myapp-staging-ttl", true)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create cluster role")
}

func TestCreateServiceAccountAndRBAC_DeleteNS_ClusterRoleBindingError(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	client.PrependReactor("create", "clusterrolebindings", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("simulated cluster role binding error")
	})

	err := CreateServiceAccountAndRBAC(ctx, client, "myapp", "staging", "ops", "myapp-staging-ttl", true)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create cluster role binding")
}

func TestCleanupRBAC_ClusterRoleBindingDeleteError(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	client.PrependReactor("delete", "clusterrolebindings", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("simulated delete error")
	})

	err := CleanupRBAC(ctx, client, "myapp", "default", "default")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to delete cluster role binding")
}

func TestCleanupRBAC_ClusterRoleDeleteError(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	client.PrependReactor("delete", "clusterroles", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("simulated delete error")
	})

	err := CleanupRBAC(ctx, client, "myapp", "default", "default")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to delete cluster role")
}

func TestCleanupRBAC_RoleBindingDeleteError(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	client.PrependReactor("delete", "rolebindings", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("simulated delete error")
	})

	err := CleanupRBAC(ctx, client, "myapp", "default", "default")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to delete role binding")
}

func TestCleanupRBAC_RoleDeleteError(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	client.PrependReactor("delete", "roles", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("simulated delete error")
	})

	// Need to bypass rolebinding delete first - add a not-found so it passes
	err := CleanupRBAC(ctx, client, "myapp", "default", "default")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to delete role")
}

func TestCleanupRBAC_ServiceAccountDeleteError(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	client.PrependReactor("delete", "serviceaccounts", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("simulated delete error")
	})

	err := CleanupRBAC(ctx, client, "myapp", "default", "default")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to delete service account")
}

func TestCleanupOrphaned_ListNamespacesError(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	client.PrependReactor("list", "namespaces", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("simulated list error")
	})

	_, err := CleanupOrphaned(ctx, client, nil, true, false)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to list namespaces")
}

func TestCleanupOrphaned_ListClusterRoleBindingsError(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	client.PrependReactor("list", "clusterrolebindings", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("simulated list error")
	})

	_, err := CleanupOrphaned(ctx, client, []string{"default"}, false, false)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to list cluster role bindings")
}

func TestCleanupOrphaned_ListClusterRolesError(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	client.PrependReactor("list", "clusterroles", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("simulated list error")
	})

	_, err := CleanupOrphaned(ctx, client, []string{"default"}, false, false)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to list cluster roles")
}

func TestCleanupOrphaned_ListRoleBindingsError(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	client.PrependReactor("list", "rolebindings", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("simulated list error")
	})

	_, err := CleanupOrphaned(ctx, client, []string{"default"}, false, false)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to list role bindings")
}

func TestCleanupOrphaned_ListRolesError(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	client.PrependReactor("list", "roles", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("simulated list error")
	})

	_, err := CleanupOrphaned(ctx, client, []string{"default"}, false, false)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to list roles")
}

func TestCleanupOrphaned_ListServiceAccountsError(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	client.PrependReactor("list", "serviceaccounts", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("simulated list error")
	})

	_, err := CleanupOrphaned(ctx, client, []string{"default"}, false, false)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to list service accounts")
}

func TestCleanupOrphaned_DeleteClusterRoleBindingError(t *testing.T) {
	ctx := context.Background()
	labels := map[string]string{
		LabelManagedBy:        LabelManagedByValue,
		LabelRelease:          "myapp",
		LabelReleaseNamespace: "staging",
		LabelCronjobNamespace: "ops",
	}
	client := fake.NewClientset(&rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "myapp-staging-ttl", Labels: labels},
		RoleRef:    rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "ClusterRole", Name: "myapp-staging-ttl"},
	})
	client.PrependReactor("delete", "clusterrolebindings", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("simulated delete error")
	})

	_, err := CleanupOrphaned(ctx, client, []string{}, false, false)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to delete cluster role binding")
}

func TestCleanupOrphaned_DeleteClusterRoleError(t *testing.T) {
	ctx := context.Background()
	labels := map[string]string{
		LabelManagedBy:        LabelManagedByValue,
		LabelRelease:          "myapp",
		LabelReleaseNamespace: "staging",
		LabelCronjobNamespace: "ops",
	}
	client := fake.NewClientset(&rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{Name: "myapp-staging-ttl", Labels: labels},
	})
	client.PrependReactor("delete", "clusterroles", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("simulated delete error")
	})

	_, err := CleanupOrphaned(ctx, client, []string{}, false, false)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to delete cluster role")
}

func TestCleanupOrphaned_DeleteRoleBindingError(t *testing.T) {
	ctx := context.Background()
	labels := map[string]string{
		LabelManagedBy:        LabelManagedByValue,
		LabelRelease:          "myapp",
		LabelReleaseNamespace: "default",
		LabelCronjobNamespace: "default",
	}
	client := fake.NewClientset(&rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "myapp-default-ttl", Namespace: "default", Labels: labels},
		RoleRef:    rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "Role", Name: "myapp-default-ttl"},
	})
	client.PrependReactor("delete", "rolebindings", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("simulated delete error")
	})

	_, err := CleanupOrphaned(ctx, client, []string{"default"}, false, false)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to delete role binding")
}

func TestCleanupOrphaned_DeleteRoleError(t *testing.T) {
	ctx := context.Background()
	labels := map[string]string{
		LabelManagedBy:        LabelManagedByValue,
		LabelRelease:          "myapp",
		LabelReleaseNamespace: "default",
		LabelCronjobNamespace: "default",
	}
	client := fake.NewClientset(&rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{Name: "myapp-default-ttl", Namespace: "default", Labels: labels},
	})
	client.PrependReactor("delete", "roles", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("simulated delete error")
	})

	_, err := CleanupOrphaned(ctx, client, []string{"default"}, false, false)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to delete role")
}

func TestCleanupOrphaned_DeleteServiceAccountError(t *testing.T) {
	ctx := context.Background()
	labels := map[string]string{
		LabelManagedBy:        LabelManagedByValue,
		LabelRelease:          "myapp",
		LabelReleaseNamespace: "default",
		LabelCronjobNamespace: "default",
	}
	client := fake.NewClientset(&corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{Name: "myapp-default-ttl", Namespace: "default", Labels: labels},
	})
	client.PrependReactor("delete", "serviceaccounts", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("simulated delete error")
	})

	_, err := CleanupOrphaned(ctx, client, []string{"default"}, false, false)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to delete service account")
}

func TestCreateServiceAccountAndRBAC_ResourceNameTooLong(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()

	err := CreateServiceAccountAndRBAC(ctx, client, "a-very-long-release-name-that-will-exceed", "a-long-namespace", "default", "sa", false)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds maximum length")
}

func TestCleanupRBAC_ResourceNameTooLong(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()

	err := CleanupRBAC(ctx, client, "a-very-long-release-name-that-will-exceed", "a-long-namespace", "default")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "exceeds maximum length")
}

func TestCreateOrUpdateServiceAccount_GetError(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	// Pre-create the SA so the create will get AlreadyExists
	_, err := client.CoreV1().ServiceAccounts("default").Create(ctx, &corev1.ServiceAccount{
		ObjectMeta: metav1.ObjectMeta{Name: "myapp-default-ttl", Namespace: "default"},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	// Now make Get fail
	client.PrependReactor("get", "serviceaccounts", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("simulated get error")
	})

	err = CreateServiceAccountAndRBAC(ctx, client, "myapp", "default", "default", "myapp-default-ttl", false)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create service account")
}

func TestCreateOrUpdateRole_GetError(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	// Pre-create the role so create returns AlreadyExists
	_, err := client.RbacV1().Roles("default").Create(ctx, &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{Name: "myapp-default-ttl", Namespace: "default"},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	// Make Get for roles fail
	client.PrependReactor("get", "roles", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("simulated get error")
	})

	err = CreateServiceAccountAndRBAC(ctx, client, "myapp", "default", "default", "myapp-default-ttl", false)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create role")
}

func TestCreateOrUpdateRoleBinding_GetError(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	// Pre-create the rolebinding
	_, err := client.RbacV1().RoleBindings("default").Create(ctx, &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "myapp-default-ttl", Namespace: "default"},
		RoleRef:    rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "Role", Name: "myapp-default-ttl"},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	client.PrependReactor("get", "rolebindings", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("simulated get error")
	})

	err = CreateServiceAccountAndRBAC(ctx, client, "myapp", "default", "default", "myapp-default-ttl", false)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create role binding")
}

func TestCreateOrUpdateClusterRole_GetError(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	// Pre-create the cluster role
	_, err := client.RbacV1().ClusterRoles().Create(ctx, &rbacv1.ClusterRole{
		ObjectMeta: metav1.ObjectMeta{Name: "myapp-staging-ttl"},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	client.PrependReactor("get", "clusterroles", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("simulated get error")
	})

	err = CreateServiceAccountAndRBAC(ctx, client, "myapp", "staging", "ops", "myapp-staging-ttl", true)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create cluster role")
}

func TestCreateOrUpdateClusterRoleBinding_GetError(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()
	// Pre-create the cluster role binding
	_, err := client.RbacV1().ClusterRoleBindings().Create(ctx, &rbacv1.ClusterRoleBinding{
		ObjectMeta: metav1.ObjectMeta{Name: "myapp-staging-ttl"},
		RoleRef:    rbacv1.RoleRef{APIGroup: "rbac.authorization.k8s.io", Kind: "ClusterRole", Name: "myapp-staging-ttl"},
	}, metav1.CreateOptions{})
	require.NoError(t, err)

	client.PrependReactor("get", "clusterrolebindings", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("simulated get error")
	})

	err = CreateServiceAccountAndRBAC(ctx, client, "myapp", "staging", "ops", "myapp-staging-ttl", true)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create cluster role binding")
}

func TestCleanupRBAC_CrossNamespaceDeleteError(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()

	// Create cross-namespace resources
	err := CreateServiceAccountAndRBAC(ctx, client, "myapp", "staging", "ops", "myapp-staging-ttl", false)
	require.NoError(t, err)

	// Make role deletion in the second namespace fail
	callCount := 0
	client.PrependReactor("delete", "roles", func(action k8stesting.Action) (bool, runtime.Object, error) {
		callCount++
		if callCount == 2 {
			return true, nil, fmt.Errorf("simulated delete error in cronjob ns")
		}
		return false, nil, nil
	})

	err = CleanupRBAC(ctx, client, "myapp", "staging", "ops")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to delete role")
}

func TestIsOrphaned_EmptyCronjobNamespace(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()

	// Labels without cronjob-namespace - should fall back to release namespace
	labels := map[string]string{
		LabelRelease:          "myapp",
		LabelReleaseNamespace: "default",
	}

	// No CronJob exists, so should be orphaned
	result := isOrphaned(ctx, client, labels)
	assert.True(t, result)
}

func TestCleanupRBAC_CrossNamespace(t *testing.T) {
	ctx := context.Background()
	client := fake.NewClientset()

	// Create cross-namespace resources
	err := CreateServiceAccountAndRBAC(ctx, client, "myapp", "staging", "ops", "myapp-staging-ttl", false)
	require.NoError(t, err)

	// Verify they exist
	_, err = client.CoreV1().ServiceAccounts("ops").Get(ctx, "myapp-staging-ttl", metav1.GetOptions{})
	require.NoError(t, err)

	_, err = client.RbacV1().Roles("staging").Get(ctx, "myapp-staging-ttl", metav1.GetOptions{})
	require.NoError(t, err)

	_, err = client.RbacV1().Roles("ops").Get(ctx, "myapp-staging-ttl", metav1.GetOptions{})
	require.NoError(t, err)

	// Clean up
	err = CleanupRBAC(ctx, client, "myapp", "staging", "ops")
	require.NoError(t, err)

	// Verify all gone
	_, err = client.CoreV1().ServiceAccounts("ops").Get(ctx, "myapp-staging-ttl", metav1.GetOptions{})
	assert.Error(t, err)

	_, err = client.RbacV1().Roles("staging").Get(ctx, "myapp-staging-ttl", metav1.GetOptions{})
	assert.Error(t, err)

	_, err = client.RbacV1().Roles("ops").Get(ctx, "myapp-staging-ttl", metav1.GetOptions{})
	assert.Error(t, err)

	_, err = client.RbacV1().RoleBindings("staging").Get(ctx, "myapp-staging-ttl", metav1.GetOptions{})
	assert.Error(t, err)

	_, err = client.RbacV1().RoleBindings("ops").Get(ctx, "myapp-staging-ttl", metav1.GetOptions{})
	assert.Error(t, err)
}
