package repo

import "testing"

func TestBranchByID_Kurkino(t *testing.T) {
	b, ok := BranchByID(3)
	if !ok {
		t.Fatal("expected branch 3")
	}
	if b.Name != "Куркино" {
		t.Fatalf("name=%q", b.Name)
	}
	if b.Address != "г. Москва, ул. Ландышевая, 14к1" {
		t.Fatalf("address=%q", b.Address)
	}
	if b.DisplayLine() != "Куркино, г. Москва, ул. Ландышевая, 14к1" {
		t.Fatalf("display=%q", b.DisplayLine())
	}
}

func TestBranchByID_Kurkino2(t *testing.T) {
	b, ok := BranchByID(496)
	if !ok || b.Name != "Куркино 2" {
		t.Fatalf("got %+v ok=%v", b, ok)
	}
}

func TestBranchByID_Kashirka(t *testing.T) {
	b, ok := BranchByID(106)
	if !ok || b.Name != "Каширка" {
		t.Fatalf("got %+v ok=%v", b, ok)
	}
}

func TestBranchByID_Unknown(t *testing.T) {
	if _, ok := BranchByID(999); ok {
		t.Fatal("expected unknown")
	}
}
