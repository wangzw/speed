package db

// OpenedMarkerName is the name the mark Open installs sits under in a handle's
// plugin table, for the external test package: a case listing the declared
// plugins on a connection built with Open leaves it out by this name.
const OpenedMarkerName = openedMarkerName

// EngineMarkerName is the name the engine marker every handle carries sits
// under in its plugin table, left out of a listing of the declared plugins by
// this name for the same reason.
const EngineMarkerName = engineMarkerName
