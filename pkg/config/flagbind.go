package config

import (
	"errors"
	"fmt"

	"github.com/spf13/pflag"
)

// BindLayer registers the arguments of one command on its flag set: the items
// of the modules named here that declared the command line as an origin. The
// command tree calls it once per command, naming the modules whose arguments
// that command contributes and no command above it does, so an empty list
// registers nothing.
//
// running names the ones among them this command actually runs, and declared
// names the modules it reached for their declarations alone. The three answer
// different questions and all three are needed. An argument of a collected
// module is not marked required, because this command never decodes the module
// and so never comes up short of the value; and giving one that is not on the
// command line gets a diagnosis that says where it does belong, which it can
// only do by knowing the module was taken in at all.
//
// An item is one argument for the whole tree: it is defined the first time a
// command takes its module in, and that same argument is registered on every
// other command that takes it in. Two commands on separate branches therefore
// share one argument, bound to the layering tool once, and a run reads its
// value from there whichever of the two it selected.
func (l *loader) BindLayer(fs *pflag.FlagSet, taking, running, declared []string) error {
	takes := make(map[string]bool, len(taking))
	for _, name := range taking {
		takes[name] = true
	}
	runs := make(map[string]bool, len(running))
	for _, name := range running {
		runs[name] = true
	}
	for _, name := range declared {
		l.declaresOnly[name] = true
	}

	for _, item := range l.m.items {
		if !takes[item.module] || !item.origins.has(OriginFlag) {
			continue
		}
		if flag, already := l.bound[item.path]; already {
			// Another command defined this argument. That one is
			// registered here rather than a second definition of
			// it: two definitions would hold two values, and only
			// the one the layering tool is bound to is ever read.
			fs.AddFlag(flag)
			continue
		}
		value, noOptDefVal := newFlagValue(item)
		fs.VarP(value, item.item.FlagName, item.item.FlagShort, flagUsage(item, runs[item.module]))
		flag := fs.Lookup(item.item.FlagName)
		flag.NoOptDefVal = noOptDefVal
		if err := l.v.BindPFlag(item.path, flag); err != nil {
			return fmt.Errorf("%w: module %q declares the command-line name %q for %q, which "+
				"could not be bound: %w", ErrConfigConflict, item.module, item.item.FlagName,
				item.path, err)
		}
		l.bound[item.path] = flag
	}
	return nil
}

// ClassifyParseError puts what the command-line library reported under this
// module's sentinels. The library's error types are what the classification
// reads, not the text they render: the text is the library's to change.
//
// Anything it does not recognise is returned as it stands. An unknown
// subcommand is one of those: the library reports it, and inventing a
// sentinel of this module's own for it would claim a failure that is not
// about configuration.
func (l *loader) ClassifyParseError(err error) error {
	var unknown *pflag.NotExistError
	if errors.As(err, &unknown) {
		return l.unknownFlagError(unknown)
	}
	var missingValue *pflag.ValueRequiredError
	if errors.As(err, &missingValue) {
		return fmt.Errorf("%w: %w", ErrMalformedCommandLine, err)
	}
	var syntax *pflag.InvalidSyntaxError
	if errors.As(err, &syntax) {
		return fmt.Errorf("%w: %w", ErrMalformedCommandLine, err)
	}
	var badValue *pflag.InvalidValueError
	if errors.As(err, &badValue) {
		return fmt.Errorf("%w: %w", ErrTypeMismatch, err)
	}
	return err
}

// unknownFlagError tells the three ways an argument the command line gives can
// fail to reach an item apart. The library knows only that nothing by that
// name is registered on this command; which of the three it is can only be
// answered by looking the name up in the manifest, and the answers lead to
// three different fixes.
func (l *loader) unknownFlagError(unknown *pflag.NotExistError) error {
	written, item := l.writtenAs(unknown)
	if item == nil {
		return fmt.Errorf("%w: the command line gives %s, which no input item declares. An item "+
			"reaches the command line only by naming itself with FlagName, and a long name is "+
			"never derived from a path", ErrUnknownKey, written)
	}
	if !item.origins.has(OriginFlag) {
		return fmt.Errorf("%w: the command line gives %s, which module %q declares as the name "+
			"of %q but does not accept from the command line: it reads %s. Add OriginFlag to "+
			"the item, or drop FlagName",
			ErrUnknownKey, written, item.module, item.path, originNames(item.origins))
	}
	if l.declaresOnly[item.module] {
		return fmt.Errorf("%w: the command line gives %s, which module %q declares for %q, and "+
			"this command takes that module in for its declarations alone, so nothing here "+
			"reads it. Add WithFlags to that inclusion to accept the argument, or take the "+
			"module in at run level if this command is meant to run it",
			ErrUnknownKey, written, item.module, item.path)
	}
	return fmt.Errorf("%w: the command line gives %s, which module %q declares for %q, and this "+
		"command does not take that module in. Give it on a command that includes the module, "+
		"or add the module to this command's Include",
		ErrUnknownKey, written, item.module, item.path)
}

// writtenAs renders the name as it appeared on the command line and looks it
// up in the manifest. A name in a group of short names is written on its own,
// since that is the name the manifest indexes.
func (l *loader) writtenAs(unknown *pflag.NotExistError) (string, *manifestItem) {
	name := unknown.GetSpecifiedName()
	if unknown.GetSpecifiedShortnames() != "" {
		return "-" + name, l.m.byShort[name]
	}
	return "--" + name, l.m.byFlag[name]
}
