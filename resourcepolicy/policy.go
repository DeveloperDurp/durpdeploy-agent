// Package resourcepolicy defines the resource limits shared by agent and server.
// Values are startup snapshots. Consumers must not read process configuration.
package resourcepolicy

type Limits struct {
	Execution Execution `yaml:"execution" json:"execution"`
	Artifact  Artifact  `yaml:"artifact" json:"artifact"`
	Staging   Staging   `yaml:"staging" json:"staging"`
	Transfer  Transfer  `yaml:"transfer" json:"transfer"`
	Container Container `yaml:"container" json:"container"`
}

type Execution struct {
	MaxRetries            int64 `yaml:"max_retries" json:"max_retries"`
	MaxTimeoutSeconds     int64 `yaml:"max_timeout_seconds" json:"max_timeout_seconds"`
	DefaultTimeoutSeconds int64 `yaml:"default_timeout_seconds" json:"default_timeout_seconds"`
}

type Artifact struct {
	DownloadBytes        int64 `yaml:"download_bytes" json:"download_bytes"`
	ExtractedBytes       int64 `yaml:"extracted_bytes" json:"extracted_bytes"`
	Files                int64 `yaml:"files" json:"files"`
	StagingNodes         int64 `yaml:"staging_nodes" json:"staging_nodes"`
	MetadataBytes        int64 `yaml:"metadata_bytes" json:"metadata_bytes"`
	FetchTimeoutSeconds  int64 `yaml:"fetch_timeout_seconds" json:"fetch_timeout_seconds"`
	DialTimeoutSeconds   int64 `yaml:"dial_timeout_seconds" json:"dial_timeout_seconds"`
	TLSTimeoutSeconds    int64 `yaml:"tls_timeout_seconds" json:"tls_timeout_seconds"`
	HeaderTimeoutSeconds int64 `yaml:"header_timeout_seconds" json:"header_timeout_seconds"`
}

type Staging struct {
	PayloadBytes int64 `yaml:"payload_bytes" json:"payload_bytes"`
	Entries      int64 `yaml:"entries" json:"entries"`
	ArchiveBytes int64 `yaml:"archive_bytes" json:"archive_bytes"`
}

type Transfer struct {
	IdleTimeoutSeconds int64 `yaml:"idle_timeout_seconds" json:"idle_timeout_seconds"`
}

type Container struct {
	PIDs                      int64   `yaml:"pids" json:"pids"`
	TmpfsBytes                int64   `yaml:"tmpfs_bytes" json:"tmpfs_bytes"`
	HelperPIDs                int64   `yaml:"helper_pids" json:"helper_pids"`
	HelperCPUs                float64 `yaml:"helper_cpus" json:"helper_cpus"`
	HelperMemoryOverheadBytes int64   `yaml:"helper_memory_overhead_bytes" json:"helper_memory_overhead_bytes"`
}

// Defaults returns a fresh value with the existing resource budgets.
func Defaults() Limits {
	return Limits{
		Execution: Execution{
			MaxRetries: 10, MaxTimeoutSeconds: 86400,
			DefaultTimeoutSeconds: 300,
		},
		Artifact: Artifact{
			DownloadBytes: 300 << 20, ExtractedBytes: 512 << 20,
			Files: 10000, StagingNodes: 19999, MetadataBytes: 16 << 20,
			FetchTimeoutSeconds: 300, DialTimeoutSeconds: 30,
			TLSTimeoutSeconds: 10, HeaderTimeoutSeconds: 30,
		},
		Staging: Staging{
			PayloadBytes: 512 << 20, Entries: 20000, ArchiveBytes: 544 << 20,
		},
		Transfer: Transfer{IdleTimeoutSeconds: 300},
		Container: Container{
			PIDs: 128, TmpfsBytes: 64 << 20, HelperPIDs: 32,
			HelperCPUs: 1, HelperMemoryOverheadBytes: 128 << 20,
		},
	}
}
