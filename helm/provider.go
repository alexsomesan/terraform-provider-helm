package helm

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log"

	"github.com/hashicorp/terraform/helper/pathorcontents"
	"github.com/hashicorp/terraform/helper/schema"
	"github.com/hashicorp/terraform/terraform"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/helm/pkg/helm"
	helm_env "k8s.io/helm/pkg/helm/environment"
	"k8s.io/helm/pkg/helm/helmpath"
	"k8s.io/helm/pkg/helm/portforwarder"
	"k8s.io/helm/pkg/kube"
	tiller_env "k8s.io/helm/pkg/tiller/environment"
)

// Provider returns the provider schema to Terraform.
func Provider() terraform.ResourceProvider {
	return &schema.Provider{
		Schema: map[string]*schema.Schema{
			"host": {
				Type:        schema.TypeString,
				Required:    true,
				DefaultFunc: schema.EnvDefaultFunc(helm_env.HostEnvVar, ""),
				Description: "Set an alternative Tiller host. The format is host:port.",
			},
			"home": {
				Type:        schema.TypeString,
				Required:    true,
				DefaultFunc: schema.EnvDefaultFunc(helm_env.HomeEnvVar, helm_env.DefaultHelmHome),
				Description: "Set an alternative location for Helm files. By default, these are stored in '~/.helm'.",
			},
			"namespace": {
				Type:        schema.TypeString,
				Optional:    true,
				Default:     tiller_env.DefaultTillerNamespace,
				Description: "Set an alternative Tiller namespace.",
			},
			"debug": {
				Type:     schema.TypeBool,
				Optional: true,
			},
			"plugins_disable": {
				Type:        schema.TypeBool,
				Optional:    true,
				DefaultFunc: schema.EnvDefaultFunc(helm_env.PluginDisableEnvVar, "true"),
				Description: "Disable plugins. Set HELM_NO_PLUGINS=1 to disable plugins.",
			},
			"insecure": {
				Type:        schema.TypeBool,
				Optional:    true,
				Description: "Whether server should be accessed without verifying the TLS certificate.",
			},
			"client_key": {
				Type:        schema.TypeString,
				Optional:    true,
				Default:     "$HELM_HOME/key.pem",
				Description: "PEM-encoded client certificate key for TLS authentication.",
			},
			"client_certificate": {
				Type:        schema.TypeString,
				Optional:    true,
				Default:     "$HELM_HOME/cert.pem",
				Description: "PEM-encoded client certificate for TLS authentication.",
			},
			"ca_certificate": {
				Type:        schema.TypeString,
				Optional:    true,
				Default:     "$HELM_HOME/ca.pem",
				Description: "PEM-encoded root certificates bundle for TLS authentication.",
			},
			"kubernetes": {
				Type:        schema.TypeList,
				Optional:    true,
				MaxItems:    1,
				Optional:    true,
				Description: "Kubernetes configuration.",
				Elem: &schema.Resource{
					Schema: map[string]*schema.Schema{
						"host": {
							Type:        schema.TypeString,
							Optional:    true,
							DefaultFunc: schema.EnvDefaultFunc("KUBE_HOST", ""),
							Description: "The hostname (in form of URI) of Kubernetes master. Can be sourced from `KUBE_HOST`.",
						},
						"username": {
							Type:        schema.TypeString,
							Optional:    true,
							DefaultFunc: schema.EnvDefaultFunc("KUBE_USER", ""),
							Description: "The username to use for HTTP basic authentication when accessing the Kubernetes master endpoint. Can be sourced from `KUBE_USER`.",
						},
						"password": {
							Type:        schema.TypeString,
							Optional:    true,
							DefaultFunc: schema.EnvDefaultFunc("KUBE_PASSWORD", ""),
							Description: "The password to use for HTTP basic authentication when accessing the Kubernetes master endpoint. Can be sourced from `KUBE_PASSWORD`.",
						},
						"insecure": {
							Type:        schema.TypeBool,
							Optional:    true,
							DefaultFunc: schema.EnvDefaultFunc("KUBE_INSECURE", false),
							Description: "Whether server should be accessed without verifying the TLS certificate. Can be sourced from `KUBE_INSECURE`.",
						},
						"client_certificate": {
							Type:        schema.TypeString,
							Optional:    true,
							DefaultFunc: schema.EnvDefaultFunc("KUBE_CLIENT_CERT_DATA", ""),
							Description: "PEM-encoded client certificate for TLS authentication. Can be sourced from `KUBE_CLIENT_CERT_DATA`.",
						},
						"client_key": {
							Type:        schema.TypeString,
							Optional:    true,
							DefaultFunc: schema.EnvDefaultFunc("KUBE_CLIENT_KEY_DATA", ""),
							Description: "PEM-encoded client certificate key for TLS authentication. Can be sourced from `KUBE_CLIENT_KEY_DATA`.",
						},
						"cluster_ca_certificate": {
							Type:        schema.TypeString,
							Optional:    true,
							DefaultFunc: schema.EnvDefaultFunc("KUBE_CLUSTER_CA_CERT_DATA", ""),
							Description: "PEM-encoded root certificates bundle for TLS authentication. Can be sourced from `KUBE_CLUSTER_CA_CERT_DATA`.",
						},
						"config_path": {
							Type:     schema.TypeString,
							Optional: true,
							DefaultFunc: schema.MultiEnvDefaultFunc(
								[]string{
									"KUBE_CONFIG",
									"KUBECONFIG",
								},
								"~/.kube/config"),
							Description: "Path to the kube config file, defaults to ~/.kube/config. Can be sourced from `KUBE_CONFIG`.",
						},
						"config_context": {
							Type:        schema.TypeString,
							Optional:    true,
							DefaultFunc: schema.EnvDefaultFunc("KUBE_CTX", ""),
							Description: "Context to choose from the config file. Can be sourced from `KUBE_CTX`.",
						},
					}},
			},
		},
		ResourcesMap: map[string]*schema.Resource{
			"helm_chart":      resourceChart(),
			"helm_repository": resourceRepository(),
		},
		ConfigureFunc: providerConfigure,
	}
}

func providerConfigure(d *schema.ResourceData) (interface{}, error) {
	return buildMeta(d)
}

type Meta struct {
	Settings         *helm_env.EnvSettings
	K8sClient        kubernetes.Interface
	K8sConfig        *rest.Config
	HelmClient       helm.Interface
	Tunnel           *kube.Tunnel
	DefaultNamespace string
}

func buildMeta(d *schema.ResourceData) (*Meta, error) {
	m := &Meta{}
	m.buildSettings(d)
	if err := m.buildK8sClient(d); err != nil {
		return nil, err
	}

	if err := m.buildTunnel(d); err != nil {
		return nil, err
	}

	if err := m.buildHelmClient(d); err != nil {
		return nil, err
	}

	return m, nil
}

func (m *Meta) buildSettings(d *schema.ResourceData) {
	m.Settings = &helm_env.EnvSettings{
		Home:            helmpath.Home(d.Get("home").(string)),
		TillerHost:      d.Get("host").(string),
		TillerNamespace: d.Get("namespace").(string),
		Debug:           d.Get("debug").(bool),
	}
}

func (m *Meta) buildK8sClient(d *schema.ResourceData) error {
	_, hasStatic := d.GetOk("kubernetes")

	ctx := d.Get("kubernetes.0.config_context").(string)
	cfg, err := kube.GetConfig(ctx).ClientConfig()
	if err != nil {
		debug("could not get Kubernetes config for context %q: %s", ctx, err)
		if hasStatic {
			return err
		}
	}

	if cfg == nil {
		cfg = &rest.Config{}
	}

	// Overriding with static configuration
	cfg.UserAgent = fmt.Sprintf("HashiCorp/1.0 Terraform/%s", terraform.VersionString())

	if v, ok := d.GetOk("kubernetes.0.host"); ok {
		cfg.Host = v.(string)
	}
	if v, ok := d.GetOk("kubernetes.0.username"); ok {
		cfg.Username = v.(string)
	}
	if v, ok := d.GetOk("kubernetes.0.password"); ok {
		cfg.Password = v.(string)
	}
	if v, ok := d.GetOk("kubernetes.0.insecure"); ok {
		cfg.Insecure = v.(bool)
	}
	if v, ok := d.GetOk("kubernetes.0.cluster_ca_certificate"); ok {
		cfg.CAData = []byte(v.(string))
	}
	if v, ok := d.GetOk("kubernetes.0.client_certificate"); ok {
		cfg.CertData = []byte(v.(string))
	}
	if v, ok := d.GetOk("kubernetes.0.client_key"); ok {
		cfg.KeyData = []byte(v.(string))
	}

	m.K8sConfig = cfg
	m.K8sClient, err = kubernetes.NewForConfig(cfg)
	if err != nil {
		return fmt.Errorf("failed to configure kubernetes config: %s", err)
	}

	return nil
}

func (m *Meta) buildTunnel(d *schema.ResourceData) error {
	if m.Settings.TillerHost != "" {
		return nil
	}

	tunnel, err := portforwarder.New(m.Settings.TillerNamespace, m.K8sClient, m.K8sConfig)
	if err != nil {
		return fmt.Errorf("error creating tunnel: %q", err)
	}

	m.Settings.TillerHost = fmt.Sprintf("localhost:%d", tunnel.Local)
	debug("Created tunnel using local port: '%d'\n", tunnel.Local)
	return nil
}

func (m *Meta) buildHelmClient(d *schema.ResourceData) error {
	options := []helm.Option{
		helm.Host(m.Settings.TillerHost),
	}

	tlscfg, err := getTLSConfig(d)
	if err != nil {
		return err
	}

	if tlscfg != nil {
		options = append(options, helm.WithTLS(tlscfg))
	}

	m.HelmClient = helm.NewClient(options...)
	return nil
}

func getTLSConfig(d *schema.ResourceData) (*tls.Config, error) {
	var err error

	keyPEMBlock, err := getContent(d, "client_key", "$HELM_HOME/key.pem")
	if err != nil {
		return nil, err
	}
	certPEMBlock, err := getContent(d, "client_certificate", "$HELM_HOME/cert.pem")
	if err != nil {
		return nil, err
	}
	if len(keyPEMBlock) == 0 && len(certPEMBlock) == 0 {
		return nil, nil
	}

	cfg := &tls.Config{
		InsecureSkipVerify: d.Get("insecure").(bool),
	}

	cert, err := tls.X509KeyPair(certPEMBlock, keyPEMBlock)
	if err != nil {
		return nil, fmt.Errorf("could not read x509 key pair: %s", err)
	}

	cfg.Certificates = []tls.Certificate{cert}

	caPEMBlock, err := getContent(d, "ca_certificate", "$HELM_HOME/ca.pem")
	if err != nil {
		return nil, err
	}

	if !cfg.InsecureSkipVerify && len(caPEMBlock) != 0 {
		cfg.RootCAs = x509.NewCertPool()
		if !cfg.RootCAs.AppendCertsFromPEM(caPEMBlock) {
			return nil, fmt.Errorf("failed to parse ca_certificate")
		}
	}

	return cfg, nil
}

func getContent(d *schema.ResourceData, key, def string) ([]byte, error) {
	filename := d.Get(key).(string)

	content, _, err := pathorcontents.Read(filename)
	if err != nil {
		return nil, err
	}

	if content == def {
		return nil, nil
	}

	return []byte(content), nil
}

func debug(format string, a ...interface{}) {
	log.Printf("[DEBUG] %s", fmt.Sprintf(format, a...))
}

var (
	tlsCaCertFile string // path to TLS CA certificate file
	tlsCertFile   string // path to TLS certificate file
	tlsKeyFile    string // path to TLS key file
	tlsVerify     bool   // enable TLS and verify remote certificates
	tlsEnable     bool   // enable TLS
)
