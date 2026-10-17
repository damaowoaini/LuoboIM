package namespaceimport

import (
	"sort"
)

// Synthetic signed controller fixture only. These helpers do not certify a
// live deployment, source policy, owner boundaries, or real Mongo coverage.
func fixtureSetProof(values []string, wanted string) []MerkleSibling {
	sorted := append([]string(nil), values...)
	sort.Strings(sorted)
	at := -1
	for i, v := range sorted {
		if v == wanted {
			at = i
		}
		sorted[i] = digest([]string{"namespace-import-set-leaf-v1", v})
	}
	if at < 0 {
		panic("synthetic proof missing leaf")
	}
	proof := []MerkleSibling{}
	for len(sorted) > 1 {
		if at%2 == 1 {
			proof = append(proof, MerkleSibling{sorted[at-1], true})
		} else if at+1 < len(sorted) {
			proof = append(proof, MerkleSibling{sorted[at+1], false})
		}
		next := []string{}
		for i := 0; i < len(sorted); i += 2 {
			if i+1 == len(sorted) {
				next = append(next, sorted[i])
			} else {
				next = append(next, digest([]string{"namespace-import-set-node-v1", sorted[i], sorted[i+1]}))
			}
		}
		at /= 2
		sorted = next
	}
	return proof
}
func fixtureSetRoot(values []string) string {
	root, e := SortedSetRoot(values)
	if e != nil {
		panic(e)
	}
	return root
}
func installSyntheticCoverage(i *Inventory, page *PageData) {
	page.OriginCoverages = nil
	page.EffectiveBoundaries = nil
	policy := digest("synthetic-history-policy")
	boundaryByIndex := map[int]EffectiveOwnerBoundary{}
	for at, w := range page.OwnerWindows {
		boundaryByIndex[at] = EffectiveOwnerBoundary{w, policy, digest([]string{"synthetic-original-owner-window", w.CID, w.OwnerID}), syntheticRefs()}
	}
	coverages := []OriginCoverage{}
	coverageValues := []string{}
	for _, r := range page.Origins {
		owners := []string{}
		boundaries := []string{}
		originals := []string{}
		for at, w := range page.OwnerWindows {
			if w.CID != r.Origin.CID {
				continue
			}
			owner := EffectiveOwnerKey(w.CID, w.OwnerID)
			owners = append(owners, owner)
			b := boundaryByIndex[at]
			value, e := EffectiveBoundarySHA256(b)
			if e != nil {
				panic(e)
			}
			boundaries = append(boundaries, value)
			originals = append(originals, b.OriginalOwnerWindowsRootSHA256)
		}
		c := OriginCoverage{HeaderSHA256: page.HeaderSHA256, InventoryBindingSHA256: i.BindingSHA256(), CID: r.Origin.CID, OriginSHA256: r.Origin.SHA256, Domain: r.Origin.Kind, OriginalWindows: uint64(len(originals)), EffectiveOwners: uint64(len(owners)), OriginalWindowSetRootSHA256: fixtureSetRoot(originals), OwnerSetRootSHA256: fixtureSetRoot(owners), EffectiveBoundarySetRootSHA256: fixtureSetRoot(boundaries), SourceCoverageRootSHA256: digest("synthetic-complete-original-source"), HistoryPolicySHA256: policy, Evidence: syntheticRefs()}
		coverages = append(coverages, c)
		coverageValues = append(coverageValues, digest(c))
	}
	i.CoverageSetRootSHA256 = fixtureSetRoot(coverageValues)
	for _, c := range coverages {
		page.OriginCoverages = append(page.OriginCoverages, OriginCoverageWitness{c, fixtureSetProof(coverageValues, digest(c))})
	}
	for at, w := range page.OwnerWindows {
		var witness OriginCoverageWitness
		for _, c := range page.OriginCoverages {
			if c.Coverage.CID == w.CID {
				witness = c
			}
		}
		owners := []string{}
		boundaries := []string{}
		for j, other := range page.OwnerWindows {
			if other.CID == w.CID {
				owners = append(owners, EffectiveOwnerKey(other.CID, other.OwnerID))
				value, _ := EffectiveBoundarySHA256(boundaryByIndex[j])
				boundaries = append(boundaries, value)
			}
		}
		boundary := boundaryByIndex[at]
		value, _ := EffectiveBoundarySHA256(boundary)
		page.EffectiveBoundaries = append(page.EffectiveBoundaries, EffectiveBoundaryWitness{boundary, witness, fixtureSetProof(owners, EffectiveOwnerKey(w.CID, w.OwnerID)), fixtureSetProof(boundaries, value)})
	}
}
