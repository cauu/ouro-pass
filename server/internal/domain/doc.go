// Package domain holds the persisted entity types, enums, and shared errors for
// the issuer (detailed §1–§8). It depends on nothing in the project so every
// layer can import it. Per decision D6, money/time/json are carried as portable
// representations in storage; in Go they surface as the natural types below.
//
// Organization: one file per entity (file name == entity name), so the file
// tree doubles as the entity index. Cross-entity enums live with their primary
// entity; shared sentinel errors live in errors.go.
package domain
