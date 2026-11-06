package booking

// SlotRef is a free start offered when the requested one was taken.
type SlotRef struct {
	Start       string `json:"start"`
	DurationMin int    `json:"duration_min"`
}

// WriteHTTP is the handler-facing result. Raw procedure text never goes here.
type WriteHTTP struct {
	Status  int
	Code    string
	Message string
	Body    any
	Slots   []SlotRef
}

// ResolveInput is a manual unknown/partial decision.
type ResolveInput struct {
	OpID       string
	State      string
	PlanningID *int64
	Note       string
	Actor      string
}
