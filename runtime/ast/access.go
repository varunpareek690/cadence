/*
 * Cadence - The resource-oriented smart contract programming language
 *
 * Copyright Dapper Labs, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *   http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package ast

import (
	"encoding/json"
	"strings"

	"github.com/onflow/cadence/runtime/errors"
)

//go:generate go run golang.org/x/tools/cmd/stringer -type=PrimitiveAccess

type Access interface {
	isAccess()
	Keyword() string
	Description() string
	String() string
	MarshalJSON() ([]byte, error)
}

type Separator uint8

const (
	Disjunction Separator = iota
	Conjunction
)

func (s Separator) String() string {
	switch s {
	case Disjunction:
		return " |"
	case Conjunction:
		return ","
	}
	panic(errors.NewUnreachableError())
}

type EntitlementSet interface {
	Entitlements() []*NominalType
	Separator() Separator
}

type ConjunctiveEntitlementSet struct {
	Elements []*NominalType `json:"ConjunctiveElements"`
}

var _ EntitlementSet = &ConjunctiveEntitlementSet{}

func (s *ConjunctiveEntitlementSet) Entitlements() []*NominalType {
	return s.Elements
}

func (s *ConjunctiveEntitlementSet) Separator() Separator {
	return Conjunction
}

func NewConjunctiveEntitlementSet(entitlements []*NominalType) *ConjunctiveEntitlementSet {
	return &ConjunctiveEntitlementSet{Elements: entitlements}
}

type DisjunctiveEntitlementSet struct {
	Elements []*NominalType `json:"DisjunctiveElements"`
}

var _ EntitlementSet = &DisjunctiveEntitlementSet{}

func (s *DisjunctiveEntitlementSet) Entitlements() []*NominalType {
	return s.Elements
}

func (s *DisjunctiveEntitlementSet) Separator() Separator {
	return Disjunction
}

func NewDisjunctiveEntitlementSet(entitlements []*NominalType) *DisjunctiveEntitlementSet {
	return &DisjunctiveEntitlementSet{Elements: entitlements}
}

type EntitlementAccess struct {
	EntitlementSet EntitlementSet
}

var _ Access = EntitlementAccess{}

func NewEntitlementAccess(entitlements EntitlementSet) EntitlementAccess {
	return EntitlementAccess{EntitlementSet: entitlements}
}

func (EntitlementAccess) isAccess() {}

func (EntitlementAccess) Description() string {
	return "entitled access"
}

func (e EntitlementAccess) entitlementsString(prefix *strings.Builder) {
	for i, entitlement := range e.EntitlementSet.Entitlements() {
		prefix.WriteString(entitlement.String())
		if i < len(e.EntitlementSet.Entitlements())-1 {
			prefix.WriteString(e.EntitlementSet.Separator().String())
		}
	}
}

func (e EntitlementAccess) String() string {
	str := &strings.Builder{}
	str.WriteString("ConjunctiveEntitlementAccess ")
	e.entitlementsString(str)
	return str.String()
}

func (e EntitlementAccess) Keyword() string {
	str := &strings.Builder{}
	str.WriteString("access(")
	e.entitlementsString(str)
	str.WriteString(")")
	return str.String()
}

func (e EntitlementAccess) MarshalJSON() ([]byte, error) {
	return json.Marshal(e.String())
}

func (e EntitlementAccess) subset(other EntitlementAccess) bool {
	otherEntitlements := other.EntitlementSet.Entitlements()
	otherSet := make(map[*NominalType]struct{}, len(otherEntitlements))
	for _, entitlement := range otherEntitlements {
		otherSet[entitlement] = struct{}{}
	}

	for _, entitlement := range e.EntitlementSet.Entitlements() {
		if _, found := otherSet[entitlement]; !found {
			return false
		}
	}

	return true
}

func (e EntitlementAccess) IsLessPermissiveThan(other Access) bool {
	switch other := other.(type) {
	case PrimitiveAccess:
		return other == AccessPublic || other == AccessPublicSettable
	case EntitlementAccess:
		return e.subset(other)
	default:
		return false
	}
}

type PrimitiveAccess uint8

// NOTE: order indicates permissiveness: from least to most permissive!

const (
	AccessNotSpecified PrimitiveAccess = iota
	AccessPrivate
	AccessContract
	AccessAccount
	AccessPublic
	AccessPublicSettable
)

func PrimitiveAccessCount() int {
	return len(_PrimitiveAccess_index) - 1
}

func (PrimitiveAccess) isAccess() {}

// TODO: remove.
//   only used by tests which are not updated yet
//   to include contract and account access

var BasicAccesses = []PrimitiveAccess{
	AccessNotSpecified,
	AccessPrivate,
	AccessPublic,
	AccessPublicSettable,
}

var AllAccesses = append(BasicAccesses[:],
	AccessContract,
	AccessAccount,
)

func (a PrimitiveAccess) Keyword() string {
	switch a {
	case AccessNotSpecified:
		return ""
	case AccessPrivate:
		return "priv"
	case AccessPublic:
		return "pub"
	case AccessPublicSettable:
		return "pub(set)"
	case AccessAccount:
		return "access(account)"
	case AccessContract:
		return "access(contract)"
	}

	panic(errors.NewUnreachableError())
}

func (a PrimitiveAccess) Description() string {
	switch a {
	case AccessNotSpecified:
		return "not specified"
	case AccessPrivate:
		return "private"
	case AccessPublic:
		return "public"
	case AccessPublicSettable:
		return "public settable"
	case AccessAccount:
		return "account"
	case AccessContract:
		return "contract"
	}

	panic(errors.NewUnreachableError())
}

func (a PrimitiveAccess) MarshalJSON() ([]byte, error) {
	return json.Marshal(a.String())
}
