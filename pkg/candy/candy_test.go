package candy

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/miekg/dns"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestServeDNS(t *testing.T) {
	t.Parallel()

	t.Run("internal request", func(t *testing.T) {
		dnsClient := &mockDNSClient{}
		dnsClients := map[string]DNSClient{
			"udp": dnsClient,
		}
		k8sClient := &mockReader{}

		candy := NewCandy(k8sClient, dnsClients, []string{".internal.local"}, []string{"8.8.8.8:53"}, logr.Discard())

		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "default",
				Name:      "test-pod",
				Labels:    map[string]string{ManagedByLabel: "vcluster-name"},
			},
			Spec: corev1.PodSpec{},
			Status: corev1.PodStatus{
				PodIP: "192.168.0.123",
			},
		}
		k8sClient.On("List", mock.Anything, mock.Anything, mock.Anything).Return(nil).Run(func(args mock.Arguments) {
			list := args.Get(1).(*corev1.PodList)
			list.Items = []corev1.Pod{*pod}
		})

		serviceIP := "10.0.0.10"
		k8sClient.On("Get", mock.Anything, mock.Anything, mock.Anything).Return(nil).Run(func(args mock.Arguments) {
			service := args.Get(2).(*corev1.Service)
			service.Spec.ClusterIP = serviceIP
		})

		query := &dns.Msg{}
		query.SetQuestion("test.internal.local.", dns.TypeA)

		resp := &dns.Msg{}
		dnsClient.On("Exchange", query, "10.0.0.10:53").Return(resp, time.Duration(0), nil)

		writer := &testResponseWriter{ipAddress: pod.Status.PodIP}
		candy.ServeDNS(writer, query)

		assert.Equal(t, resp, writer.response)
	})

	t.Run("external request", func(t *testing.T) {
		dnsClient := &mockDNSClient{}
		dnsClients := map[string]DNSClient{
			"udp": dnsClient,
		}
		k8sClient := &mockReader{}

		candy := NewCandy(k8sClient, dnsClients, []string{".internal.local"}, []string{"8.8.8.8:53"}, logr.Discard())

		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "default",
				Name:      "test-pod",
				Labels:    map[string]string{ManagedByLabel: "vcluster-name"},
			},
			Spec: corev1.PodSpec{},
			Status: corev1.PodStatus{
				PodIP: "192.168.0.123",
			},
		}
		k8sClient.On("List", mock.Anything, mock.Anything, mock.Anything).Return(nil).Run(func(args mock.Arguments) {
			list := args.Get(1).(*corev1.PodList)
			list.Items = []corev1.Pod{*pod}
		})

		query := &dns.Msg{}
		query.SetQuestion("test.external.com.", dns.TypeA)

		resp := &dns.Msg{}
		dnsClient.On("Exchange", query, "8.8.8.8:53").Return(resp, time.Duration(0), nil)

		writer := &testResponseWriter{ipAddress: pod.Status.PodIP}
		candy.ServeDNS(writer, query)

		assert.Equal(t, resp, writer.response)
	})

	t.Run("non-vcluster managed pod", func(t *testing.T) {
		dnsClient := &mockDNSClient{}
		dnsClients := map[string]DNSClient{
			"udp": dnsClient,
		}
		k8sClient := &mockReader{}

		candy := NewCandy(k8sClient, dnsClients, []string{".internal.local"}, []string{"8.8.8.8:53"}, logr.Discard())

		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "default",
				Name:      "test-pod",
			},
			Spec: corev1.PodSpec{},
			Status: corev1.PodStatus{
				PodIP: "192.168.0.123",
			},
		}
		k8sClient.On("List", mock.Anything, mock.Anything, mock.Anything).Return(nil).Run(func(args mock.Arguments) {
			list := args.Get(1).(*corev1.PodList)
			list.Items = []corev1.Pod{*pod}
		})

		query := &dns.Msg{}
		query.SetQuestion("test.internal.local.", dns.TypeA)

		writer := &testResponseWriter{ipAddress: pod.Status.PodIP}
		candy.ServeDNS(writer, query)

		assert.NotNil(t, writer.response)
		assert.Equal(t, dns.RcodeRefused, writer.response.Rcode)
	})
}

func TestIsInternalDNSRequest(t *testing.T) {
	tests := []struct {
		name       string
		req        *dns.Msg
		internal   []string
		wantResult bool
	}{
		{
			name: "empty request",
			req:  &dns.Msg{},
			internal: []string{
				".internal.domain",
				".svc.cluster.local",
			},
			wantResult: false,
		},
		{
			name: "single matching suffix",
			req: &dns.Msg{
				Question: []dns.Question{
					{Name: "example.internal.domain."},
				},
			},
			internal: []string{
				".internal.domain",
				".svc.cluster.local",
			},
			wantResult: true,
		},
		{
			name: "multiple questions with one match",
			req: &dns.Msg{
				Question: []dns.Question{
					{Name: "example.external.domain."},
					{Name: "example.svc.cluster.local."},
				},
			},
			internal: []string{
				".svc.cluster.local",
			},
			wantResult: true,
		},
		{
			name: "multiple questions with no match",
			req: &dns.Msg{
				Question: []dns.Question{
					{Name: "example.external.domain."},
					{Name: "another.example."},
				},
			},
			internal: []string{
				".internal.domain",
			},
			wantResult: false,
		},
		{
			name: "exact matching name",
			req: &dns.Msg{
				Question: []dns.Question{
					{Name: "exactmatch.com."},
				},
			},
			internal: []string{
				"exactmatch.com",
			},
			wantResult: true,
		},
		{
			name: "empty internal suffixes",
			req: &dns.Msg{
				Question: []dns.Question{
					{Name: "example.internal.domain."},
				},
			},
			internal:   []string{},
			wantResult: false,
		},
		{
			name: "question without name",
			req: &dns.Msg{
				Question: []dns.Question{
					{Name: ""},
				},
			},
			internal: []string{
				".internal.domain",
			},
			wantResult: false,
		},
		{
			name: "internal suffix has leading dot for exact match",
			req: &dns.Msg{
				Question: []dns.Question{
					{Name: "exact.internal.match."},
				},
			},
			internal: []string{
				".internal.match",
			},
			wantResult: true,
		},
		{
			name: "internal suffix without leading dot matches suffix",
			req: &dns.Msg{
				Question: []dns.Question{
					{Name: "sub.example.org."},
				},
			},
			internal: []string{
				"example.org",
			},
			wantResult: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isInternalDNSRequest(tt.req, tt.internal)
			if result != tt.wantResult {
				t.Errorf("isInternalDNSRequest() = %v, want %v", result, tt.wantResult)
			}
		})
	}
}

type mockDNSClient struct {
	mock.Mock
}

func (m *mockDNSClient) Exchange(r *dns.Msg, address string) (*dns.Msg, time.Duration, error) {
	args := m.Called(r, address)
	return args.Get(0).(*dns.Msg), args.Get(1).(time.Duration), args.Error(2)
}

type mockReader struct {
	mock.Mock
}

func (m *mockReader) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	args := m.Called(ctx, key, obj)
	return args.Error(0)
}

func (m *mockReader) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	args := m.Called(ctx, list, opts)
	return args.Error(0)
}

type testResponseWriter struct {
	response  *dns.Msg
	ipAddress string
}

func (w *testResponseWriter) LocalAddr() net.Addr {
	return nil
}

func (w *testResponseWriter) RemoteAddr() net.Addr {
	return &net.UDPAddr{IP: net.ParseIP(w.ipAddress), Port: 1234}
}

func (w *testResponseWriter) WriteMsg(msg *dns.Msg) error {
	w.response = msg
	return nil
}

func (w *testResponseWriter) Write([]byte) (int, error) {
	return 0, nil
}

func (w *testResponseWriter) Close() error {
	return nil
}

func (w *testResponseWriter) TsigStatus() error {
	return nil
}

func (w *testResponseWriter) TsigTimersOnly(bool) {}

func (w *testResponseWriter) Hijack() {}
