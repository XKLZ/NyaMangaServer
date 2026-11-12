package model

type ClientIdentity struct {
	InstanceID string `json:"instance_id"`
	CacheEpoch string `json:"cache_epoch"`
}

type ClientScopeLibrary struct {
	ID       string `json:"id"`
	Role     string `json:"role"`
	Disabled bool   `json:"disabled"`
}

type ClientConfiguration struct {
	Identity          ClientIdentity
	HealthMaintenance bool
	BackgroundHold    bool
	Libraries         []ClientScopeLibrary
}
