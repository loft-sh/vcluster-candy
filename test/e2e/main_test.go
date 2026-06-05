package e2e

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/e2e-framework/klient/k8s/resources"
	"sigs.k8s.io/e2e-framework/pkg/env"
	"sigs.k8s.io/e2e-framework/pkg/envconf"
	"sigs.k8s.io/e2e-framework/pkg/envfuncs"
	"sigs.k8s.io/e2e-framework/support/kind"
	"sigs.k8s.io/e2e-framework/third_party/helm"
)

const (
	vclusterVersion       = "v0.34.1"
	candyImage            = "vcluster-candy:e2e"
	candyClusterIP        = "10.96.0.42"
	tenantName            = "tenant1"
	tenantNamespace       = "vcluster-tenant1"
	externalTestServiceIP = "1.2.3.4"
)

var testenv env.Environment

func TestMain(m *testing.M) {
	cfg, err := envconf.NewFromFlags()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to build envconf: %v\n", err)
		os.Exit(1)
	}

	testenv = env.NewWithConfig(cfg)
	clusterName := envconf.RandomName("candy", 16)

	testenv.Setup(
		envfuncs.CreateCluster(kind.NewProvider(), clusterName),
		applyCorefile,
		buildCandyImage,
		envfuncs.LoadDockerImageToCluster(clusterName, candyImage),
		installCandyChart,
		createTenantCluster,
	)

	testenv.Finish(
		envfuncs.DestroyCluster(clusterName),
	)

	os.Exit(testenv.Run(m))
}

func applyCorefile(ctx context.Context, cfg *envconf.Config) (context.Context, error) {
	// read Corefile from testdata
	corefile, err := os.ReadFile(filepath.Join(repoRoot(), "test", "e2e", "testdata", "Corefile"))
	if err != nil {
		return ctx, err
	}

	// update Corefile in coredns ConfigMap
	client, err := cfg.NewClient()
	if err != nil {
		return ctx, err
	}

	namespace := "kube-system"
	configMapName := "coredns"

	var cm corev1.ConfigMap
	err = client.Resources(namespace).Get(ctx, configMapName, namespace, &cm)
	if err != nil {
		return ctx, err
	}

	cm.Data["Corefile"] = string(corefile)

	err = client.Resources(namespace).Update(ctx, &cm)
	if err != nil {
		return ctx, err
	}

	// restart kube-dns pods to pick up the updated Corefile
	var podList corev1.PodList
	err = client.Resources(namespace).List(ctx, &podList, resources.WithLabelSelector("k8s-app=kube-dns"))
	if err != nil {
		return ctx, err
	}

	for _, pod := range podList.Items {
		err = client.Resources(namespace).Delete(ctx, &pod)
		if err != nil {
			return ctx, err
		}
	}

	return ctx, nil
}

func buildCandyImage(ctx context.Context, cfg *envconf.Config) (context.Context, error) {
	cmd := exec.CommandContext(ctx, "docker", "build", "-t", candyImage, repoRoot())
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return ctx, fmt.Errorf("docker build %s: %w", candyImage, err)
	}
	return ctx, nil
}

func installCandyChart(ctx context.Context, cfg *envconf.Config) (context.Context, error) {
	manager := helm.New(cfg.KubeconfigFile())
	err := manager.RunInstall(
		helm.WithName("vcluster-candy"),
		helm.WithChart(filepath.Join(repoRoot(), "chart")),
		helm.WithNamespace("vcluster-candy"),
		helm.WithArgs(
			"--set", "image.repository=vcluster-candy",
			"--set", "image.tag=e2e",
			"--set", "image.pullPolicy=Never",
			"--set", "service.clusterIP="+candyClusterIP,
			"--create-namespace",
		),
		helm.WithWait(),
		helm.WithTimeout("4m"),
	)
	if err != nil {
		return ctx, fmt.Errorf("helm install vcluster-candy: %w", err)
	}
	return ctx, nil
}

func createTenantCluster(ctx context.Context, cfg *envconf.Config) (context.Context, error) {
	manager := helm.New(cfg.KubeconfigFile())
	err := manager.RunInstall(
		helm.WithName(tenantName),
		helm.WithChart("vcluster"),
		helm.WithVersion(vclusterVersion),
		helm.WithNamespace(tenantNamespace),
		helm.WithArgs(
			"-f", filepath.Join(repoRoot(), "test", "e2e", "testdata", "vcluster.yaml"),
			"--repo", "https://charts.loft.sh",
			"--create-namespace",
		),
		helm.WithWait(),
		helm.WithTimeout("4m"),
	)
	if err != nil {
		return ctx, fmt.Errorf("tenant cluster create: %w", err)
	}
	return ctx, nil
}

func repoRoot() string {
	_, thisFile, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(thisFile), "..", "..")
}
