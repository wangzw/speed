package config

// given reports whether any source above the defaults layer gave the item a
// value: the primary config source, the command line, the environment.
//
// The layering tool does not publish which layer a key's value came from, so
// the three sources are asked one by one. Each leg answers for one of them.
// The prototypes are not among them and never count as given -- a carrier
// struct's field always holds something, and counting it would make "nobody
// gave a value" and "somebody gave the zero value" the same answer. The host
// defaults document is not among them either, for a different reason: it is
// an explicit declaration by the host, and documentGiven judges it on its own
// rather than folding it into what this answer means.
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
	// The environment leg reads the snapshot the values come from, so an item
	// counts as given exactly when the environment layer supplies its value.
	_, set := l.envValue(item)
	return set
}

// documentGiven reports whether the host defaults document names the item's
// path with a value.
//
// The document is an explicit declaration by the host, the same act as writing
// the key in the configuration file, so it satisfies a required item the way
// the sources above the defaults layer do. What it is not is a fourth leg of
// given: it sits in the defaults layer, and only the required judgment treats
// it as somebody having given a value.
//
// The read is over the document as the parser gave it, through primaryValue,
// so a path written flat and one written nested mean the same thing. A key
// written as nothing is not a value, for the reason it is not one anywhere
// else: nil is what every layer reads as unset, and the prototype's field
// value stays the default it was.
func (l *loader) documentGiven(item *manifestItem) bool {
	value, found := primaryValue(l.defaults, item.path)
	return found && value != nil
}
