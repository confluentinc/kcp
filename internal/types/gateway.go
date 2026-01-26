package types

type GatewayResource struct {
	Spec GatewaySpec `yaml:"spec"`
}

type GatewaySpec struct {
	StreamingDomains []StreamingDomain `yaml:"streamingDomains"`
	Routes           []Route           `yaml:"routes"`
}

type StreamingDomain struct {
	Name string `yaml:"name"`
}

type Route struct {
	Name            string               `yaml:"name"`
	StreamingDomain RouteStreamingDomain `yaml:"streamingDomain"`
	Security        RouteSecurity        `yaml:"security"`
}

type RouteStreamingDomain struct {
	Name              string `yaml:"name"`
	BootstrapServerId string `yaml:"bootstrapServerId"`
}

type RouteSecurity struct {
	Auth    string              `yaml:"auth"`
	Client  RouteSecurityConfig `yaml:"client"`
	Cluster RouteSecurityConfig `yaml:"cluster"`
}

type RouteSecurityConfig struct {
	Authentication RouteSecurityAuthentication `yaml:"authentication"`
}

type RouteSecurityAuthentication struct {
	Type string `yaml:"type"`
}
