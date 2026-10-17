package canary

const (
	pinnedXrayVersion         = "26.7.28"
	pinnedXrayCommit          = "5ca6f4b7d4dc20a881d4330e498892697627ec0c"
	pinnedSourceArchiveURL    = "https://github.com/XTLS/Xray-core/archive/refs/tags/v26.7.28.tar.gz"
	pinnedSourceArchiveSHA256 = "f7e2426b267f24aabdc72868bf85ebe100df9cce50ed90595a5c959ad188bf70"
	pinnedBinaryArchiveURL    = "https://github.com/XTLS/Xray-core/releases/download/v26.7.28/Xray-linux-64.zip"
	pinnedBinaryArchiveSHA256 = "8195d909f1109b8f3d99eefe401a3c451d7bf4af71f24d3815420f77e5dd2a40"
	pinnedBinarySHA256        = "64d46afb80adea1bf97a0d467e83f4a9ac1ebd0995891e84bca3f1a1d1affb1d"
)

type XrayProvenance struct {
	Version             string `json:"version"`
	Commit              string `json:"commit"`
	SourceArchiveURL    string `json:"source_archive_url"`
	SourceArchiveSHA256 string `json:"source_archive_sha256"`
	BinaryArchiveURL    string `json:"binary_archive_url"`
	BinaryArchiveSHA256 string `json:"binary_archive_sha256"`
	BinarySHA256        string `json:"binary_sha256"`
}

func pinnedProvenance() XrayProvenance {
	return XrayProvenance{pinnedXrayVersion, pinnedXrayCommit, pinnedSourceArchiveURL, pinnedSourceArchiveSHA256, pinnedBinaryArchiveURL, pinnedBinaryArchiveSHA256, pinnedBinarySHA256}
}
