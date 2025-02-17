// Copyright 2021 FerretDB Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package shareddata provides data for tests and benchmarks.
package shareddata

import (
	"maps"
	"slices"
)

// unset represents a field that should not be set.
var unset = struct{}{}

// AllProviders returns all providers in random order.
func AllProviders() Providers {
	providers := []Provider{
		Scalars,

		Doubles,
		Decimal128s,
		OverflowVergeDoubles,
		SmallDoubles,
		Strings,
		Binaries,
		ObjectIDs,
		Bools,
		DateTimes,
		Nulls,
		Regexes,
		Int32s,
		Timestamps,
		Int64s,
		Unsets,
		ObjectIDKeys,

		Composites,
		PostgresEdgeCases,

		DocumentsDoubles,
		DocumentsStrings,
		DocumentsDocuments,
		DocumentsDeeplyNested,

		ArrayStrings,
		ArrayDoubles,
		ArrayInt32s,
		ArrayRegexes,
		ArrayDocuments,

		Mixed,
		ArrayAndDocuments,
	}

	// check that names are unique and randomize order
	res := make(map[string]Provider, len(providers))
	for _, p := range providers {
		n := p.Name()
		if _, ok := res[n]; ok {
			panic("duplicate provider name: " + n)
		}

		res[n] = p
	}

	return slices.Collect(maps.Values(res))
}

// Providers are array of providers.
type Providers []Provider

// Remove specified providers and return remaining providers.
func (ps Providers) Remove(removeProviders ...Provider) Providers {
	res := make([]Provider, 0, len(ps))

	for _, p := range ps {
		keep := true

		for _, removeProvider := range removeProviders {
			if p == removeProvider {
				keep = false
				break
			}
		}

		if keep {
			res = append(res, p)
		}
	}

	return res
}

// Docs returns all documents from given providers.
func Docs(providers ...Provider) []any {
	var res []any
	for _, p := range providers {
		for _, doc := range p.Docs() {
			res = append(res, doc)
		}
	}
	return res
}
