package config

import (
	"errors"
	"fmt"
	"slices"

	"github.com/spf13/pflag"
)

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

// Setting a face marks the argument given as well as changing its value, because the argument is what the layering tool and the required rule read.
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
		if item := l.m.byFlag[badValue.GetFlag().Name]; item != nil && item.item.Sensitive {
			return fmt.Errorf("%w: the command line gives --%s a value it does not take, for %q. "+
				"The value is not repeated here, since the item is sensitive",
				ErrTypeMismatch, item.item.FlagName, item.path)
		}
		return fmt.Errorf("%w: %w", ErrTypeMismatch, err)
	}
	return err
}

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

func (l *loader) writtenAs(unknown *pflag.NotExistError) (string, *manifestItem) {
	name := unknown.GetSpecifiedName()
	if unknown.GetSpecifiedShortnames() != "" {
		return "-" + name, l.m.byShort[name]
	}
	return "--" + name, l.m.byFlag[name]
}
