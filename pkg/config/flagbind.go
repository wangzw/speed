package config

import (
	"errors"
	"fmt"
	"slices"

	"github.com/spf13/pflag"
)

// BindLayer registers the arguments of one command on its flag set: the items
// of the modules named here that declared the command line as an origin. The
// command tree calls it once per command, naming the modules whose arguments
// that command shows in a way no command above it already does, so an empty
// list registers nothing.
//
// running names the ones among them this command actually runs. An argument of
// a module the command only collects from is not marked required, because this
// command never decodes the module and so never comes up short of the value.
//
// An item holds one value for the whole tree, and each command that carries it
// shows it with a face of its own. The value is defined the first time any
// command takes the module in and is bound to the layering tool once, so a run
// reads it from there whichever command it selected and wherever on the path
// the argument was written. The face is per command because what the help
// output says about the argument is: the same required item is one a run of
// the command that runs its module cannot start without, and one a run of a
// command that only collects its declarations can leave out.
func (l *loader) BindLayer(fs *pflag.FlagSet, taking, running []string) error {
	takes := make(map[string]bool, len(taking))
	for _, name := range taking {
		takes[name] = true
	}
	runs := make(map[string]bool, len(running))
	for _, name := range running {
		runs[name] = true
	}

	for _, item := range l.m.items {
		if !takes[item.module] || !item.origins.has(OriginFlag) {
			continue
		}
		arg, err := l.argumentOf(item)
		if err != nil {
			return err
		}
		fs.AddFlag(faceOf(arg, flagUsage(item, runs[item.module])))
	}
	return nil
}

// argumentOf gives the one argument an item is across the tree, defining it
// and binding it to the layering tool the first time it is asked for.
//
// The argument is registered on no command itself. Every command registers a
// face of it instead, and a face forwards what it is given here: two
// definitions would hold two values, and only the one the layering tool is
// bound to is ever read.
func (l *loader) argumentOf(item *manifestItem) (*pflag.Flag, error) {
	if arg, defined := l.bound[item.path]; defined {
		return arg, nil
	}
	value, noOptDefVal := newFlagValue(item)
	arg := &pflag.Flag{
		Name:        item.item.FlagName,
		Shorthand:   item.item.FlagShort,
		Usage:       item.item.Description,
		Value:       value,
		DefValue:    value.String(),
		NoOptDefVal: noOptDefVal,
	}
	if err := l.v.BindPFlag(item.path, arg); err != nil {
		return nil, fmt.Errorf("%w: module %q declares the command-line name %q for %q, which "+
			"could not be bound: %w", ErrConfigConflict, item.module, item.item.FlagName,
			item.path, err)
	}
	l.bound[item.path] = arg
	return arg, nil
}

// faceOf is the argument as one command shows it: the same name, the same
// default and the same value, under the usage text that command's help prints.
func faceOf(arg *pflag.Flag, usage string) *pflag.Flag {
	return &pflag.Flag{
		Name:        arg.Name,
		Shorthand:   arg.Shorthand,
		Usage:       usage,
		Value:       sharedValue{arg: arg},
		DefValue:    arg.DefValue,
		NoOptDefVal: arg.NoOptDefVal,
	}
}

// sharedValue is the value of a face: it is the argument's own. Setting it
// marks the argument given as well as the face, since the argument is what the
// layering tool and the required rule read.
type sharedValue struct {
	arg *pflag.Flag
}

var _ pflag.Value = sharedValue{}

func (v sharedValue) String() string { return v.arg.Value.String() }
func (v sharedValue) Type() string   { return v.arg.Value.Type() }

func (v sharedValue) Set(s string) error {
	if err := v.arg.Value.Set(s); err != nil {
		return err
	}
	v.arg.Changed = true
	return nil
}

// ClassifyParseError puts what the command-line library reported under this
// module's sentinels. The library's error types are what the classification
// reads, not the text they render: the text is the library's to change.
//
// collected names the modules the command the library was parsing reached for
// their declarations alone. It is that command's and no other's: which fix an
// unknown argument calls for depends on what the command being run took in,
// and a sibling that collects the module says nothing about a command that
// does not take it in at all.
//
// Anything it does not recognise is returned as it stands. An unknown
// subcommand is one of those: the library reports it, and inventing a
// sentinel of this module's own for it would claim a failure that is not
// about configuration.
func (l *loader) ClassifyParseError(err error, collected []string) error {
	var unknown *pflag.NotExistError
	if errors.As(err, &unknown) {
		return l.unknownFlagError(unknown, collected)
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
		// The library's text quotes the value it refused, which a sensitive
		// item keeps out of every diagnostic: this one is printed to
		// standard error.
		if item := l.m.byFlag[badValue.GetFlag().Name]; item != nil && item.item.Sensitive {
			return fmt.Errorf("%w: the command line gives --%s a value it does not take, for %q. "+
				"The value is not repeated here, since the item is sensitive",
				ErrTypeMismatch, item.item.FlagName, item.path)
		}
		return fmt.Errorf("%w: %w", ErrTypeMismatch, err)
	}
	return err
}

// unknownFlagError tells the three ways an argument the command line gives can
// fail to reach an item apart. The library knows only that nothing by that
// name is registered on this command; which of the three it is can only be
// answered by looking the name up in the manifest, and the answers lead to
// three different fixes.
func (l *loader) unknownFlagError(unknown *pflag.NotExistError, collected []string) error {
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
	if slices.Contains(collected, item.module) {
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
