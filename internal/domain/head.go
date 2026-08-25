package domain

// HeadMode selects which canonical Ethereum head anchors a synchronization.
type HeadMode string

const (
	HeadLatest    HeadMode = "latest"
	HeadSafe      HeadMode = "safe"
	HeadFinalized HeadMode = "finalized"
)
