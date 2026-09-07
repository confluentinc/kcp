package grouping

// unionFind is a disjoint-set structure over topic names, used to compute the
// connected components of the "co-occurs in a transaction" relation.
type unionFind struct {
	parent map[string]string
	rank   map[string]int
}

func newUnionFind() *unionFind {
	return &unionFind{parent: map[string]string{}, rank: map[string]int{}}
}

func (u *unionFind) add(x string) {
	if _, ok := u.parent[x]; !ok {
		u.parent[x] = x
	}
}

func (u *unionFind) find(x string) string {
	for u.parent[x] != x {
		u.parent[x] = u.parent[u.parent[x]] // path halving
		x = u.parent[x]
	}
	return x
}

func (u *unionFind) union(a, b string) {
	ra, rb := u.find(a), u.find(b)
	if ra == rb {
		return
	}
	if u.rank[ra] < u.rank[rb] {
		ra, rb = rb, ra
	}
	u.parent[rb] = ra
	if u.rank[ra] == u.rank[rb] {
		u.rank[ra]++
	}
}
