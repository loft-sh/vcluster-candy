package main

import (
	"flag"
	"os"
	"strings"

	"github.com/loft-sh/vcluster-candy/pkg/candy"
	"github.com/loft-sh/vcluster-candy/pkg/dnsserver"
	"github.com/loft-sh/vcluster-candy/pkg/util"
	"github.com/miekg/dns"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

var (
	scheme = runtime.NewScheme()
)

func init() {
	utilruntime.Must(corev1.AddToScheme(scheme))
}

func main() {
	log := ctrl.Log.WithName("setup")

	var dnsAddr string
	var metricsAddr string
	var probeAddr string
	var internalDomainsString string
	var resolvconf string
	flag.StringVar(&dnsAddr, "dns-bind-address", ":53", "The address the dns server binds to.")
	flag.StringVar(&metricsAddr, "metrics-bind-address", ":9153", "The address the metrics endpoint binds to.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.StringVar(&internalDomainsString, "internal-domain", "cluster.local", "The internal domains to use for the DNS server.")
	flag.StringVar(&resolvconf, "resolvconf", "/etc/resolv.conf", "The resolv.conf file to use for the DNS server.")

	// logger setup
	opts := zap.Options{Development: true}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	// new controller-runtime manager
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress: metricsAddr,
		},
		HealthProbeBindAddress: probeAddr,
	})
	if err != nil {
		log.Error(err, "Failed to create manager")
		os.Exit(1)
	}

	// parse internal domains
	internalDomains := strings.Split(internalDomainsString, ",")

	// get DNS servers from resolv.conf
	hostDNSServers, err := util.GetResolvConfDNSServers(resolvconf)
	if err != nil {
		log.Error(err, "Failed to get DNS servers from resolv.conf")
		os.Exit(1)
	}

	// dns clients
	dnsClients := map[string]candy.DNSClient{
		"udp": &dns.Client{Net: "udp"},
		"tcp": &dns.Client{Net: "tcp"},
	}

	// new dns handler
	candyHandler := candy.NewCandy(
		mgr.GetClient(),
		dnsClients,
		internalDomains,
		hostDNSServers,
		mgr.GetLogger().WithName("candy"))

	// setup dns handler with manager
	if err := candyHandler.SetupWithManager(mgr); err != nil {
		log.Error(err, "Failed to setup DNS handler")
		os.Exit(1)
	}

	for _, protocol := range []string{"udp", "tcp"} {
		// new dns server using the dns handler
		dnsServer := dnsserver.NewServer(dnsAddr, protocol, candyHandler, mgr.GetLogger().WithName("dns-"+protocol))

		// add dns server to manager
		if err = mgr.Add(dnsServer); err != nil {
			log.Error(err, "Failed to add DNS server to manager")
			os.Exit(1)
		}
	}

	// add health checks
	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		log.Error(err, "Failed to set up health check")
		os.Exit(1)
	}

	// add readiness checks
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		log.Error(err, "Failed to set up ready check")
		os.Exit(1)
	}

	// start manager
	log.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		log.Error(err, "Failed to run manager")
		os.Exit(1)
	}
}
