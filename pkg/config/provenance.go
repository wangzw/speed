package config

import "os"

// given reports whether any source above the defaults gave the item a value,
// which is exactly the question a required item asks.
//
// The layering tool does not publish which layer a key's value came from, so
// the three sources are asked one by one. Each leg answers for one of them:
// the primary config source, the command line, the environment. The default
// layer is not among them and never counts as given -- a carrier struct's
// field always holds something, and counting it would make "nobody gave a
// value" and "somebody gave the zero value" the same answer.
//
// The command-line leg reads the argument the item was registered as, which
// exists only once BindLayer has run for the level that carries it.
func (l *loader) given(item *manifestItem) bool {
	if l.v.InConfig(item.path) {
		return true
	}
	if flag := l.bound[item.path]; flag != nil && flag.Changed {
		return true
	}
	if item.envName == "" {
		return false
	}
	// An empty variable counts, which is the same reading AllowEmptyEnv gives
	// the layering tool: somebody who set the variable did give a value.
	_, set := os.LookupEnv(item.envName)
	return set
}
