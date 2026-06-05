package e2e

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/loft-sh/vcluster/pkg/util/translate"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/e2e-framework/klient/k8s"
	"sigs.k8s.io/e2e-framework/klient/k8s/resources"
	"sigs.k8s.io/e2e-framework/klient/wait"
	"sigs.k8s.io/e2e-framework/klient/wait/conditions"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/features"
)

func TestDNSRouting(t *testing.T) {
	internal := features.New("internal queries route to the tenant kube-dns").
		Setup(waitForServiceClusterIp(translatedName("test"), tenantNamespace)).
		Assess("test.default.svc.cluster.local resolves to the tenant test service IP", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			r := cfg.Client().Resources(tenantNamespace)
			var testService corev1.Service
			if err := r.Get(ctx, translatedName("test"), tenantNamespace, &testService); err != nil {
				t.Fatal(err)
			}

			assertResolves(ctx, t, cfg, translatedName("test"), "test.default.svc.cluster.local", testService.Spec.ClusterIP)
			return ctx
		}).
		Feature()

	external := features.New("external queries route to the host upstream").
		Assess("test.external.com resolves via the host resolver", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			assertResolves(ctx, t, cfg, translatedName("test"), "test.external.com", externalTestServiceIP)
			return ctx
		}).
		Feature()

	hostNetwork := features.New("queries from host-network pods are refused").
		Setup(waitForServiceClusterIp(translatedName("test"), tenantNamespace)).
		Assess("a lookup is refused", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			assertRefused(ctx, t, cfg, translatedName("test-with-host-network"), "test.default.svc.cluster.local")
			return ctx
		}).
		Feature()

	unmanaged := features.New("queries from unmanaged pods are refused").
		Setup(createUnmanagedPod("unmanaged")).
		Assess("a lookup is refused", func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
			assertRefused(ctx, t, cfg, "unmanaged", "test.default.svc.cluster.local")
			return ctx
		}).
		Teardown(deleteUnmanagedPod("unmanaged")).
		Feature()

	testenv.Test(t, internal, external, unmanaged, hostNetwork)
}

func translatedName(name string) string {
	return translate.SafeConcatName(name, "x", "default", "x", tenantName)
}

func nslookup(ctx context.Context, r *resources.Resources, pod, query string) (string, error) {
	var out bytes.Buffer
	err := r.ExecInPod(ctx, tenantNamespace, pod, "test", []string{"nslookup", query, candyClusterIP}, &out, &out)
	return out.String(), err
}

func assertResolves(ctx context.Context, t *testing.T, cfg *envconf.Config, pod, query, want string) {
	t.Helper()
	r := cfg.Client().Resources(tenantNamespace)

	var last string
	err := wait.For(func(ctx context.Context) (bool, error) {
		out, err := nslookup(ctx, r, pod, query)
		last = out
		if err != nil {
			return false, nil
		}
		return strings.Contains(out, want), nil
	}, wait.WithContext(ctx), wait.WithTimeout(90*time.Second), wait.WithInterval(3*time.Second))

	if err != nil {
		t.Fatalf("expected %q to resolve to %q from pod %q; last nslookup output:\n%s", query, want, pod, last)
	}
}

func assertRefused(ctx context.Context, t *testing.T, cfg *envconf.Config, pod, query string) {
	t.Helper()
	r := cfg.Client().Resources(tenantNamespace)

	var last string
	err := wait.For(func(ctx context.Context) (bool, error) {
		out, cmdErr := nslookup(ctx, r, pod, query)
		last = out
		if cmdErr == nil {
			return false, fmt.Errorf("query %q unexpectedly resolved from pod %q:\n%s", query, pod, out)
		}
		low := strings.ToLower(out)
		if strings.Contains(low, "refused") || strings.Contains(low, "can't find") {
			return true, nil
		}
		// Some other transient failure (e.g. pod networking not ready) — retry.
		return false, nil
	}, wait.WithContext(ctx), wait.WithTimeout(90*time.Second), wait.WithInterval(3*time.Second))

	if err != nil {
		t.Fatalf("expected %q to be refused from pod %q; last nslookup output:\n%s\nerror: %v", query, pod, last, err)
	}
}

func createUnmanagedPod(name string) features.Func {
	return func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
		r := cfg.Client().Resources(tenantNamespace)
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: tenantNamespace,
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{{
					Name:    "test",
					Image:   "busybox:1.36",
					Command: []string{"sleep", "900"},
				}},
			},
		}

		if err := r.Create(ctx, pod); err != nil {
			t.Fatalf("create client pod %q: %v", name, err)
		}
		if err := wait.For(
			conditions.New(r).PodRunning(pod),
			wait.WithContext(ctx),
			wait.WithTimeout(2*time.Minute),
			wait.WithInterval(2*time.Second),
		); err != nil {
			t.Fatalf("waiting for client pod %q to run: %v", name, err)
		}
		return ctx
	}
}

func deleteUnmanagedPod(name string) features.Func {
	return func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
		r := cfg.Client().Resources(tenantNamespace)
		pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: tenantNamespace}}
		if err := r.Delete(ctx, pod); err != nil {
			t.Logf("delete client pod %q: %v", name, err)
		}
		return ctx
	}
}

func waitForServiceClusterIp(name string, namespace string) features.Func {
	return func(ctx context.Context, t *testing.T, cfg *envconf.Config) context.Context {
		s := corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}}

		err := wait.For(conditions.New(cfg.Client().Resources()).ResourceMatch(&s, func(object k8s.Object) bool {
			current := object.(*corev1.Service)
			return current.Spec.ClusterIP != ""
		}), wait.WithContext(ctx), wait.WithTimeout(time.Minute))
		if err != nil {
			t.Fatal(err)
		}

		return ctx
	}
}
