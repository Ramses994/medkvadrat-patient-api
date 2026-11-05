package repo

import "go/types"

func forbiddenMedialogTime(typ types.Type) bool {
	seen := map[types.Type]bool{}
	var walk func(types.Type) bool
	walk = func(t types.Type) bool {
		if t == nil || seen[t] {
			return false
		}
		seen[t] = true
		switch u := t.(type) {
		case *types.Named:
			obj := u.Obj()
			if obj != nil && obj.Pkg() != nil {
				path, name := obj.Pkg().Path(), obj.Name()
				if (path == "time" && name == "Time") || (path == "database/sql" && name == "NullTime") {
					return true
				}
			}
			return walk(u.Underlying())
		case *types.Pointer:
			return walk(u.Elem())
		default:
			return false
		}
	}
	return walk(typ)
}
