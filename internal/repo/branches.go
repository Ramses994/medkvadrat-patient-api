package repo

// Branch is a clinic location (FM_ORG) for human-readable reminder/dashboard text.
type Branch struct {
	Name    string
	Address string
}

// Branches maps FM_ORG_ID → display name and address (three active clinic branches).
var Branches = map[int]Branch{
	3:   {Name: "Куркино", Address: "г. Москва, ул. Ландышевая, 14к1"},
	496: {Name: "Куркино 2", Address: "г. Москва, ул. Воротынская, 4"},
	106: {Name: "Каширка", Address: "г. Москва, Каширское шоссе, 74к1"},
}

// BranchByID returns the known branch for an FM_ORG_ID.
func BranchByID(id int) (Branch, bool) {
	b, ok := Branches[id]
	return b, ok
}

// DisplayLine returns "Name, Address" for reminder text.
func (b Branch) DisplayLine() string {
	if b.Address == "" {
		return b.Name
	}
	return b.Name + ", " + b.Address
}
