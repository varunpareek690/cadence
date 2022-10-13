/*
 * Cadence - The resource-oriented smart contract programming language
 *
 * Copyright 2019-2022 Dapper Labs, Inc.
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

package sema

import (
	"github.com/onflow/cadence/runtime/ast"
	"github.com/onflow/cadence/runtime/common"
	"github.com/onflow/cadence/runtime/errors"
)

// VisitInterfaceDeclaration checks the given interface declaration.
//
// NOTE: This function assumes that the interface type was previously declared using
// `declareInterfaceType` and exists in `checker.Elaboration.InterfaceDeclarationTypes`,
// and that the members and nested declarations for the interface type were declared
// through `declareInterfaceMembers`.
func (checker *Checker) VisitInterfaceDeclaration(declaration *ast.InterfaceDeclaration) (_ struct{}) {

	const kind = ContainerKindInterface

	interfaceType := checker.Elaboration.InterfaceDeclarationTypes[declaration]
	if interfaceType == nil {
		panic(errors.NewUnreachableError())
	}

	checker.containerTypes[interfaceType] = true
	defer func() {
		checker.containerTypes[interfaceType] = false
	}()

	checker.checkDeclarationAccessModifier(
		declaration.Access,
		declaration.DeclarationKind(),
		declaration.StartPos,
		true,
	)

	inheritedMembers := map[string]*Member{}
	inheritedTypes := map[string]Type{}

	interfaceType.ExplicitInterfaceConformances.ForeachDistinct(
		func(_, conformance *InterfaceType) bool {
			checker.checkInterfaceConformance(
				declaration,
				interfaceType,
				conformance,
				inheritedMembers,
				inheritedTypes,
			)

			return true
		},
	)

	// NOTE: functions are checked separately
	checker.checkFieldsAccessModifier(declaration.Members.Fields())

	checker.checkNestedIdentifiers(declaration.Members)

	// Activate new scope for nested types

	checker.typeActivations.Enter()
	defer checker.typeActivations.Leave(declaration.EndPosition)

	// Declare nested types

	checker.declareInterfaceNestedTypes(declaration)

	checker.checkInitializers(
		declaration.Members.Initializers(),
		declaration.Members.Fields(),
		interfaceType,
		declaration.DeclarationKind(),
		declaration.DeclarationDocString(),
		interfaceType.InitializerParameters,
		kind,
		nil,
	)

	checker.checkUnknownSpecialFunctions(declaration.Members.SpecialFunctions())
	checker.checkSpecialFunctionDefaultImplementation(
		declaration,
		declaration.DeclarationKind().Name(),
	)

	checker.checkInterfaceFunctions(
		declaration.Members.Functions(),
		interfaceType,
		declaration.DeclarationKind(),
		declaration.DeclarationDocString(),
	)

	fieldPositionGetter := func(name string) ast.Position {
		return interfaceType.FieldPosition(name, declaration)
	}

	checker.checkResourceFieldNesting(
		interfaceType.Members,
		interfaceType.CompositeKind,
		fieldPositionGetter,
	)

	checker.checkDestructors(
		declaration.Members.Destructors(),
		declaration.Members.FieldsByIdentifier(),
		interfaceType.Members,
		interfaceType,
		declaration.DeclarationKind(),
		declaration.DeclarationDocString(),
		kind,
	)

	// NOTE: visit interfaces first
	// DON'T use `nestedDeclarations`, because of non-deterministic order

	for _, nestedInterface := range declaration.Members.Interfaces() {
		ast.AcceptDeclaration[struct{}](nestedInterface, checker)
	}

	for _, nestedComposite := range declaration.Members.Composites() {
		// Composite declarations nested in interface declarations are type requirements,
		// i.e. they should be checked like interfaces

		checker.visitCompositeDeclaration(nestedComposite, kind)
	}

	return
}

// declareInterfaceNestedTypes declares the types nested in an interface.
// It is used when declaring the interface's members (`declareInterfaceMembers`)
// and checking the interface declaration (`VisitInterfaceDeclaration`).
//
// It assumes the types were previously added to the elaboration in `InterfaceNestedDeclarations`,
// and the type for the declaration was added to the elaboration in `InterfaceDeclarationTypes`.
func (checker *Checker) declareInterfaceNestedTypes(
	declaration *ast.InterfaceDeclaration,
) {

	interfaceType := checker.Elaboration.InterfaceDeclarationTypes[declaration]
	nestedDeclarations := checker.Elaboration.InterfaceNestedDeclarations[declaration]

	interfaceType.NestedTypes.Foreach(func(name string, nestedType Type) {
		nestedDeclaration := nestedDeclarations[name]

		identifier := nestedDeclaration.DeclarationIdentifier()
		if identifier == nil {
			// It should be impossible to have a nested declaration
			// that does not have an identifier

			panic(errors.NewUnreachableError())
		}

		_, err := checker.typeActivations.declareType(typeDeclaration{
			identifier:               *identifier,
			ty:                       nestedType,
			declarationKind:          nestedDeclaration.DeclarationKind(),
			access:                   nestedDeclaration.DeclarationAccess(),
			docString:                nestedDeclaration.DeclarationDocString(),
			allowOuterScopeShadowing: false,
		})
		checker.report(err)
	})
}

func (checker *Checker) checkInterfaceFunctions(
	functions []*ast.FunctionDeclaration,
	selfType Type,
	declarationKind common.DeclarationKind,
	selfDocString string,
) {
	for _, function := range functions {
		// NOTE: new activation, as function declarations
		// shouldn't be visible in other function declarations,
		// and `self` is is only visible inside function

		func() {
			checker.enterValueScope()
			defer checker.leaveValueScope(function.EndPosition, false)

			checker.declareSelfValue(selfType, selfDocString)

			mustExit := false
			checkResourceLoss := false

			if function.FunctionBlock != nil {
				if function.FunctionBlock.HasStatements() {
					mustExit = true
					checkResourceLoss = true
				} else if function.FunctionBlock.PreConditions.IsEmpty() &&
					function.FunctionBlock.PostConditions.IsEmpty() {

					checker.report(
						&InvalidImplementationError{
							Pos:             function.FunctionBlock.StartPosition(),
							ContainerKind:   declarationKind,
							ImplementedKind: common.DeclarationKindFunction,
						},
					)
				}
			}

			checker.visitFunctionDeclaration(
				function,
				functionDeclarationOptions{
					mustExit:          mustExit,
					declareFunction:   false,
					checkResourceLoss: checkResourceLoss,
				},
			)

		}()
	}
}

// declareInterfaceType declares the type for the given interface declaration
// and records it in the elaboration. It also recursively declares all types
// for all nested declarations.
//
// NOTE: The function does *not* declare any members
//
// See `declareInterfaceMembers` for the declaration of the interface type members.
// See `VisitInterfaceDeclaration` for the checking of the interface declaration.
func (checker *Checker) declareInterfaceType(declaration *ast.InterfaceDeclaration) *InterfaceType {

	identifier := declaration.Identifier

	interfaceType := &InterfaceType{
		Location:      checker.Location,
		Identifier:    identifier.Identifier,
		CompositeKind: declaration.CompositeKind,
		NestedTypes:   &StringTypeOrderedMap{},
		Members:       &StringMemberOrderedMap{},
	}

	variable, err := checker.typeActivations.declareType(typeDeclaration{
		identifier:               identifier,
		ty:                       interfaceType,
		declarationKind:          declaration.DeclarationKind(),
		access:                   declaration.Access,
		docString:                declaration.DocString,
		allowOuterScopeShadowing: false,
	})
	checker.report(err)
	if checker.PositionInfo != nil && variable != nil {
		checker.recordVariableDeclarationOccurrence(
			identifier.Identifier,
			variable,
		)
	}

	// Resolve conformances
	interfaceType.ExplicitInterfaceConformances =
		checker.explicitInterfaceConformances(declaration, interfaceType)

	checker.Elaboration.InterfaceDeclarationTypes[declaration] = interfaceType
	checker.Elaboration.InterfaceTypeDeclarations[interfaceType] = declaration

	if !declaration.CompositeKind.SupportsInterfaces() {
		checker.report(
			&InvalidInterfaceDeclarationError{
				CompositeKind: declaration.CompositeKind,
				Range:         ast.NewRangeFromPositioned(checker.memoryGauge, declaration.Identifier),
			},
		)
	}

	// Activate new scope for nested declarations

	checker.typeActivations.Enter()
	defer checker.typeActivations.Leave(declaration.EndPosition)

	checker.enterValueScope()
	defer checker.leaveValueScope(declaration.EndPosition, false)

	// Check and declare nested types

	nestedDeclarations, nestedInterfaceTypes, nestedCompositeTypes :=
		checker.declareNestedDeclarations(
			declaration.CompositeKind,
			declaration.DeclarationKind(),
			declaration.Members.Composites(),
			declaration.Members.Interfaces(),
		)

	checker.Elaboration.InterfaceNestedDeclarations[declaration] = nestedDeclarations

	for _, nestedInterfaceType := range nestedInterfaceTypes {
		interfaceType.NestedTypes.Set(nestedInterfaceType.Identifier, nestedInterfaceType)
		nestedInterfaceType.SetContainerType(interfaceType)
	}

	for _, nestedCompositeType := range nestedCompositeTypes {
		interfaceType.NestedTypes.Set(nestedCompositeType.Identifier, nestedCompositeType)
		nestedCompositeType.SetContainerType(interfaceType)
	}

	return interfaceType
}

// declareInterfaceMembers declares the members for the given interface declaration,
// and recursively for all nested declarations.
//
// NOTE: This function assumes that the interface type and the nested declarations' types
// were previously declared using `declareInterfaceType` and exists
// in the elaboration's `InterfaceDeclarationTypes` and `InterfaceNestedDeclarations` fields.
func (checker *Checker) declareInterfaceMembers(declaration *ast.InterfaceDeclaration) {

	interfaceType := checker.Elaboration.InterfaceDeclarationTypes[declaration]
	if interfaceType == nil {
		panic(errors.NewUnreachableError())
	}

	// Activate new scope for nested declarations

	checker.typeActivations.Enter()
	defer checker.typeActivations.Leave(declaration.EndPosition)

	checker.enterValueScope()
	defer checker.leaveValueScope(declaration.EndPosition, false)

	// Declare nested types

	checker.declareInterfaceNestedTypes(declaration)

	// Declare members

	members, fields, origins := checker.defaultMembersAndOrigins(
		declaration.Members,
		interfaceType,
		ContainerKindInterface,
		declaration.DeclarationKind(),
	)

	if interfaceType.CompositeKind == common.CompositeKindContract {
		checker.checkMemberStorability(members)
	}

	interfaceType.Members = members
	interfaceType.Fields = fields
	if checker.PositionInfo != nil {
		checker.PositionInfo.recordMemberOrigins(interfaceType, origins)
	}

	// NOTE: determine initializer parameter types while nested types are in scope,
	// and after declaring nested types as the initializer may use nested type in parameters

	interfaceType.InitializerParameters =
		checker.initializerParameters(declaration.Members.Initializers())

	// Declare nested declarations' members

	for _, nestedInterfaceDeclaration := range declaration.Members.Interfaces() {
		checker.declareInterfaceMembers(nestedInterfaceDeclaration)
	}

	for _, nestedCompositeDeclaration := range declaration.Members.Composites() {
		checker.declareCompositeMembersAndValue(nestedCompositeDeclaration, ContainerKindInterface)
	}
}

func (checker *Checker) checkInterfaceConformance(
	interfaceDeclaration *ast.InterfaceDeclaration,
	interfaceType *InterfaceType,
	conformance *InterfaceType,
	inheritedMembers map[string]*Member,
	inheritedNestedTypes map[string]Type,
) {

	// Ensure the composite kinds match, e.g. a structure shouldn't be able
	// to conform to a resource interface
	checker.checkConformanceKindMatch(interfaceDeclaration, interfaceType, conformance)

	// Check for member (functions and fields) conflicts

	conformance.Members.Foreach(func(name string, conformanceMember *Member) {

		// Check if the members coming from other conformances have conflicts.
		if conflictingMember, ok := inheritedMembers[name]; ok {
			conflictingInterface := conflictingMember.ContainerType.(*InterfaceType)
			checker.checkDuplicateInterfaceMembers(
				conformance,
				conformanceMember,
				conflictingInterface,
				conflictingMember,
				func() ast.Range {
					return ast.NewRangeFromPositioned(checker.memoryGauge, interfaceDeclaration.Identifier)
				},
			)
		}

		inheritedMembers[name] = conformanceMember

		// Check if the members coming from the current declaration have conflicts.
		declarationMember, ok := interfaceType.Members.Get(name)
		if !ok {
			return
		}

		checker.checkDuplicateInterfaceMembers(
			interfaceType,
			declarationMember,
			conformance,
			conformanceMember,
			func() ast.Range {
				return ast.NewRangeFromPositioned(checker.memoryGauge, declarationMember.Identifier)
			},
		)
	})

	// Check for nested type conflicts

	reportTypeConflictError := func(typeName string, typ CompositeKindedType, otherType Type) {
		otherCompositeType, ok := otherType.(CompositeKindedType)
		if !ok {
			return
		}

		_, isInterface := typ.(*InterfaceType)
		_, isOtherTypeInterface := otherCompositeType.(*InterfaceType)

		checker.report(&InterfaceMemberConflictError{
			InterfaceType:            interfaceType,
			ConflictingInterfaceType: conformance,
			MemberName:               typeName,
			MemberKind:               typ.GetCompositeKind().DeclarationKind(isInterface),
			ConflictingMemberKind:    otherCompositeType.GetCompositeKind().DeclarationKind(isOtherTypeInterface),
			Range: ast.NewRangeFromPositioned(
				checker.memoryGauge,
				interfaceDeclaration.Identifier,
			),
		})
	}

	conformance.NestedTypes.Foreach(func(name string, typeRequirement Type) {
		compositeType, ok := typeRequirement.(CompositeKindedType)
		if !ok {
			return
		}

		// Check if the type definitions coming from other conformances have conflicts.
		if inheritedType, ok := inheritedNestedTypes[name]; ok {
			inheritedCompositeType, ok := inheritedType.(CompositeKindedType)
			if !ok {
				return
			}

			reportTypeConflictError(name, compositeType, inheritedCompositeType)
		}

		inheritedNestedTypes[name] = typeRequirement

		// Check if the type definitions coming from the current declaration have conflicts.
		nestedType, ok := interfaceType.NestedTypes.Get(name)
		if ok {
			reportTypeConflictError(name, compositeType, nestedType)
		}
	})
}

func (checker *Checker) checkDuplicateInterfaceMembers(
	interfaceType *InterfaceType,
	interfaceMember *Member,
	conflictingInterfaceType *InterfaceType,
	conflictingMember *Member,
	getRange func() ast.Range,
) {

	reportMemberConflictError := func() {
		checker.report(&InterfaceMemberConflictError{
			InterfaceType:            interfaceType,
			ConflictingInterfaceType: conflictingInterfaceType,
			MemberName:               interfaceMember.Identifier.Identifier,
			MemberKind:               interfaceMember.DeclarationKind,
			ConflictingMemberKind:    conflictingMember.DeclarationKind,
			Range:                    getRange(),
		})
	}

	// Check if the two members have identical signatures.
	// If yes, they are allowed, but subject to the conditions below.
	// If not, report an error.
	if !checker.memberSatisfied(interfaceType, interfaceMember, conflictingMember) {
		reportMemberConflictError()
		return
	}

	// If they are functions with same name, check whether any of them have
	// default implementations or conditions. i.e: Anything more than just the signature.
	// It is invalid to have default impl / conditions, because it creates ambiguity.

	if interfaceMember.HasConditions ||
		interfaceMember.HasImplementation ||
		conflictingMember.HasConditions ||
		conflictingMember.HasImplementation {

		reportMemberConflictError()
	}
}
