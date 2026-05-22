package candy

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"github.com/loft-sh/vcluster-candy/pkg/util"
	"github.com/miekg/dns"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const IndexByPodIP = "indexbypodip"
const IndexByNamespacedName = "indexbynamespacedname"
const ManagedByLabel = "vcluster.loft.sh/managed-by"
const DNSPort = "53"

type DNSClient interface {
	Exchange(m *dns.Msg, address string) (r *dns.Msg, rtt time.Duration, err error)
}

type Candy struct {
	k8sClient       client.Reader
	dnsClient       DNSClient
	internalDomains []string
	hostDNSServers  []string
	logger          logr.Logger
}

func NewCandy(k8sClient client.Reader, dnsClient DNSClient, internalDomains []string, hostDNSServers []string, logger logr.Logger) *Candy {
	return &Candy{
		k8sClient:       k8sClient,
		dnsClient:       dnsClient,
		internalDomains: util.NormalizeSuffixes(internalDomains),
		hostDNSServers:  hostDNSServers,
		logger:          logger,
	}
}

var ErrUnknownPod = errors.New("unknown pod")

// ServeDNS implements the dns.Handler interface.
func (c *Candy) ServeDNS(w dns.ResponseWriter, r *dns.Msg) {
	log := c.logger.WithValues("requestId", r.Id)

	// get upstream DNS servers
	dnsServers, err := c.getDNSServersForRequest(context.Background(), w, r)
	if err != nil {
		if errors.Is(err, ErrUnknownPod) {
			log.Info("request is not managed by vcluster, refusing")
			errorFunc(w, r, dns.RcodeRefused)
			return
		}

		log.Error(err, "failed to get DNS servers for request")
		errorFunc(w, r, dns.RcodeServerFailure)
		return
	}

	// forward request
	for _, dnsServer := range dnsServers {
		resp, _, err := c.dnsClient.Exchange(r, dnsServer)
		if err != nil {
			log.Error(err, "upstream forward failed")
			continue
		}

		// return response
		if err := w.WriteMsg(resp); err != nil {
			log.V(1).Error(err, "failed to write DNS response")
		}
		return
	}

	log.Info("failed to serveDNS")
	errorFunc(w, r, dns.RcodeServerFailure)
}

func (c *Candy) getDNSServersForRequest(ctx context.Context, w dns.ResponseWriter, r *dns.Msg) ([]string, error) {
	// get remote IP
	podIP, err := remoteIP(w)
	if err != nil {
		return nil, fmt.Errorf("failed to get remote IP: %w", err)
	}

	// get pod by IP
	pod, err := getPodByIP(ctx, c.k8sClient, podIP)
	if err != nil {
		return nil, fmt.Errorf("failed to get pod by IP: %w", err)
	}

	// get vcluster name
	vclusterName := pod.Labels[ManagedByLabel]
	if vclusterName == "" {
		return nil, fmt.Errorf("pod %s/%s is not managed by vcluster: %w", pod.Namespace, pod.Name, ErrUnknownPod)
	}

	// if internal, use vcluster internal DNS
	if isInternalDNSRequest(r, c.internalDomains) {
		clusterIP, err := getVClusterDNSServiceClusterIP(ctx, c.k8sClient, pod.Namespace, vclusterName)
		if err != nil {
			return nil, fmt.Errorf("failed to get vcluster DNS service: %w", err)
		}

		return []string{net.JoinHostPort(clusterIP, DNSPort)}, nil
	}

	// use resolv.conf DNS entries
	return c.hostDNSServers, nil
}

// SetupWithManager is used to create pod and service indexers
func (c *Candy) SetupWithManager(mgr ctrl.Manager) error {
	err := mgr.GetFieldIndexer().IndexField(context.Background(), &corev1.Pod{}, IndexByPodIP, func(rawObj client.Object) []string {
		pod := rawObj.(*corev1.Pod)

		// only index pods managed by vcluster
		if _, ok := pod.Labels[ManagedByLabel]; !ok {
			return nil
		}

		ips := sets.NewString()
		if pod.Status.PodIP != "" {
			ips.Insert(pod.Status.PodIP)
		}
		for _, podIP := range pod.Status.PodIPs {
			if podIP.IP != "" {
				ips.Insert(podIP.IP)
			}
		}
		return ips.List()
	})
	if err != nil {
		return fmt.Errorf("failed to setup IndexByPodIP index: %w", err)
	}

	// even though this index is not used directly, it must be registered to ensure that the services are loaded at startup.
	err = mgr.GetFieldIndexer().IndexField(context.Background(), &corev1.Service{}, IndexByNamespacedName, func(rawObj client.Object) []string {
		service := rawObj.(*corev1.Service)
		return []string{service.Name + "." + service.Namespace}
	})
	if err != nil {
		return fmt.Errorf("failed to setup IndexByNamespacedName index: %w", err)
	}

	return nil
}

func errorFunc(w dns.ResponseWriter, r *dns.Msg, rcode int) {
	answer := new(dns.Msg)
	answer.SetRcode(r, rcode)
	_ = w.WriteMsg(answer)
}

func isInternalDNSRequest(req *dns.Msg, internal []string) bool {
	for _, q := range req.Question {
		name := strings.ToLower(strings.TrimSuffix(q.Name, "."))
		if name == "" {
			continue
		}
		for _, sfx := range internal {
			bare := strings.TrimPrefix(sfx, ".")
			if name == bare || strings.HasSuffix(name, sfx) {
				return true
			}
		}
	}

	return false
}

func remoteIP(w dns.ResponseWriter) (string, error) {
	remoteAddr := w.RemoteAddr()
	if remoteAddr == nil {
		return "", fmt.Errorf("response writer has no remote address")
	}

	srcIP, _, err := net.SplitHostPort(remoteAddr.String())
	if err != nil {
		return "", fmt.Errorf("failed to split remote address: %w", err)
	}

	return srcIP, nil
}

func getPodByIP(ctx context.Context, c client.Reader, ip string) (*corev1.Pod, error) {
	var podList corev1.PodList
	if err := c.List(ctx, &podList, client.MatchingFields{IndexByPodIP: ip}); err != nil {
		return nil, fmt.Errorf("failed to lookup pod by IP: %w", err)
	}

	// bail if we don't have exactly one pod
	if len(podList.Items) != 1 {
		return nil, fmt.Errorf("expected exactly one pod for IP %s, got %d: %w", ip, len(podList.Items), ErrUnknownPod)
	}

	return &podList.Items[0], nil
}

func getVClusterDNSServiceClusterIP(ctx context.Context, c client.Reader, namespace string, name string) (string, error) {
	nn := types.NamespacedName{
		Name:      util.SafeConcatName("kube-dns", "x", "kube-system", "x", name),
		Namespace: namespace,
	}

	var service corev1.Service
	if err := c.Get(ctx, nn, &service); err != nil {
		return "", fmt.Errorf("failed to get vcluster DNS service: %w", err)
	}

	return service.Spec.ClusterIP, nil
}
