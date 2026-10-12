package sso

// The change values this module writes into the authn.idp.changed payload. A
// consumer uses them to drop the cached provider or service provider of an IdP;
// the row is the authority, so the change is read as a hint and nothing more.
const (
	changeCreated          = "created"
	changeUpdated          = "updated"
	changeEnabled          = "enabled"
	changeDisabled         = "disabled"
	changeDeleted          = "deleted"
	changeDomainVerified   = "domain-verified"
	changeDomainUnverified = "domain-unverified"
	changeDomainRemoved    = "domain-removed"
)
