oclif-hello-world
=================

oclif example Hello World CLI

[![oclif](https://img.shields.io/badge/cli-oclif-brightgreen.svg)](https://oclif.io)
[![Version](https://img.shields.io/npm/v/oclif-hello-world.svg)](https://npmjs.org/package/oclif-hello-world)
[![CircleCI](https://circleci.com/gh/oclif/hello-world/tree/main.svg?style=shield)](https://circleci.com/gh/oclif/hello-world/tree/main)
[![Downloads/week](https://img.shields.io/npm/dw/oclif-hello-world.svg)](https://npmjs.org/package/oclif-hello-world)
[![License](https://img.shields.io/npm/l/oclif-hello-world.svg)](https://github.com/oclif/hello-world/blob/main/package.json)

<!-- toc -->
* [Usage](#usage)
* [Commands](#commands)
<!-- tocstop -->
# Usage
<!-- usage -->
```sh-session
$ npm install -g flagsmith-cli
$ flagsmith COMMAND
running command...
$ flagsmith (--version)
flagsmith-cli/0.0.0 darwin-arm64 node-v16.13.2
$ flagsmith --help [COMMAND]
USAGE
  $ flagsmith COMMAND
...
```
<!-- usagestop -->
# Commands
<!-- commands -->
* [`flagsmith hello PERSON`](#flagsmith-hello-person)
* [`flagsmith hello world`](#flagsmith-hello-world)
* [`flagsmith help [COMMAND]`](#flagsmith-help-command)
* [`flagsmith plugins`](#flagsmith-plugins)
* [`flagsmith plugins:install PLUGIN...`](#flagsmith-pluginsinstall-plugin)
* [`flagsmith plugins:inspect PLUGIN...`](#flagsmith-pluginsinspect-plugin)
* [`flagsmith plugins:install PLUGIN...`](#flagsmith-pluginsinstall-plugin-1)
* [`flagsmith plugins:link PLUGIN`](#flagsmith-pluginslink-plugin)
* [`flagsmith plugins:uninstall PLUGIN...`](#flagsmith-pluginsuninstall-plugin)
* [`flagsmith plugins:uninstall PLUGIN...`](#flagsmith-pluginsuninstall-plugin-1)
* [`flagsmith plugins:uninstall PLUGIN...`](#flagsmith-pluginsuninstall-plugin-2)
* [`flagsmith plugins update`](#flagsmith-plugins-update)

## `flagsmith hello PERSON`

Say hello

```
USAGE
  $ flagsmith hello [PERSON] -f <value>

ARGUMENTS
  PERSON  Person to say hello to

FLAGS
  -f, --from=<value>  (required) Whom is saying hello

DESCRIPTION
  Say hello

EXAMPLES
  $ oex hello friend --from oclif
  hello friend from oclif! (./src/commands/hello/index.ts)
```

_See code: [dist/commands/hello/index.ts](https://github.com/Flagsmith/flagsmith-cli/blob/v0.0.0/dist/commands/hello/index.ts)_

## `flagsmith hello world`

Say hello world

```
USAGE
  $ flagsmith hello world

DESCRIPTION
  Say hello world

EXAMPLES
  $ oex hello world
  hello world! (./src/commands/hello/world.ts)
```

## `flagsmith help [COMMAND]`

Display help for flagsmith.

```
USAGE
  $ flagsmith help [COMMAND] [-n]

ARGUMENTS
  COMMAND  Command to show help for.

FLAGS
  -n, --nested-commands  Include all nested commands in the output.

DESCRIPTION
  Display help for flagsmith.
```

_See code: [@oclif/plugin-help](https://github.com/oclif/plugin-help/blob/v5.1.12/src/commands/help.ts)_

## `flagsmith plugins`

List installed plugins.

```
USAGE
  $ flagsmith plugins [--core]

FLAGS
  --core  Show core plugins.

DESCRIPTION
  List installed plugins.

EXAMPLES
  $ flagsmith plugins
```

_See code: [@oclif/plugin-plugins](https://github.com/oclif/plugin-plugins/blob/v2.0.11/src/commands/plugins/index.ts)_

## `flagsmith plugins:install PLUGIN...`

Installs a plugin into the CLI.

```
USAGE
  $ flagsmith plugins:install PLUGIN...

ARGUMENTS
  PLUGIN  Plugin to install.

FLAGS
  -f, --force    Run yarn install with force flag.
  -h, --help     Show CLI help.
  -v, --verbose

DESCRIPTION
  Installs a plugin into the CLI.

  Can be installed from npm or a git url.

  Installation of a user-installed plugin will override a core plugin.

  e.g. If you have a core plugin that has a 'hello' command, installing a user-installed plugin with a 'hello' command
  will override the core plugin implementation. This is useful if a user needs to update core plugin functionality in
  the CLI without the need to patch and update the whole CLI.

ALIASES
  $ flagsmith plugins add

EXAMPLES
  $ flagsmith plugins:install myplugin 

  $ flagsmith plugins:install https://github.com/someuser/someplugin

  $ flagsmith plugins:install someuser/someplugin
```

## `flagsmith plugins:inspect PLUGIN...`

Displays installation properties of a plugin.

```
USAGE
  $ flagsmith plugins:inspect PLUGIN...

ARGUMENTS
  PLUGIN  [default: .] Plugin to inspect.

FLAGS
  -h, --help     Show CLI help.
  -v, --verbose

DESCRIPTION
  Displays installation properties of a plugin.

EXAMPLES
  $ flagsmith plugins:inspect myplugin
```

## `flagsmith plugins:install PLUGIN...`

Installs a plugin into the CLI.

```
USAGE
  $ flagsmith plugins:install PLUGIN...

ARGUMENTS
  PLUGIN  Plugin to install.

FLAGS
  -f, --force    Run yarn install with force flag.
  -h, --help     Show CLI help.
  -v, --verbose

DESCRIPTION
  Installs a plugin into the CLI.

  Can be installed from npm or a git url.

  Installation of a user-installed plugin will override a core plugin.

  e.g. If you have a core plugin that has a 'hello' command, installing a user-installed plugin with a 'hello' command
  will override the core plugin implementation. This is useful if a user needs to update core plugin functionality in
  the CLI without the need to patch and update the whole CLI.

ALIASES
  $ flagsmith plugins add

EXAMPLES
  $ flagsmith plugins:install myplugin 

  $ flagsmith plugins:install https://github.com/someuser/someplugin

  $ flagsmith plugins:install someuser/someplugin
```

## `flagsmith plugins:link PLUGIN`

Links a plugin into the CLI for development.

```
USAGE
  $ flagsmith plugins:link PLUGIN

ARGUMENTS
  PATH  [default: .] path to plugin

FLAGS
  -h, --help     Show CLI help.
  -v, --verbose

DESCRIPTION
  Links a plugin into the CLI for development.

  Installation of a linked plugin will override a user-installed or core plugin.

  e.g. If you have a user-installed or core plugin that has a 'hello' command, installing a linked plugin with a 'hello'
  command will override the user-installed or core plugin implementation. This is useful for development work.

EXAMPLES
  $ flagsmith plugins:link myplugin
```

## `flagsmith plugins:uninstall PLUGIN...`

Removes a plugin from the CLI.

```
USAGE
  $ flagsmith plugins:uninstall PLUGIN...

ARGUMENTS
  PLUGIN  plugin to uninstall

FLAGS
  -h, --help     Show CLI help.
  -v, --verbose

DESCRIPTION
  Removes a plugin from the CLI.

ALIASES
  $ flagsmith plugins unlink
  $ flagsmith plugins remove
```

## `flagsmith plugins:uninstall PLUGIN...`

Removes a plugin from the CLI.

```
USAGE
  $ flagsmith plugins:uninstall PLUGIN...

ARGUMENTS
  PLUGIN  plugin to uninstall

FLAGS
  -h, --help     Show CLI help.
  -v, --verbose

DESCRIPTION
  Removes a plugin from the CLI.

ALIASES
  $ flagsmith plugins unlink
  $ flagsmith plugins remove
```

## `flagsmith plugins:uninstall PLUGIN...`

Removes a plugin from the CLI.

```
USAGE
  $ flagsmith plugins:uninstall PLUGIN...

ARGUMENTS
  PLUGIN  plugin to uninstall

FLAGS
  -h, --help     Show CLI help.
  -v, --verbose

DESCRIPTION
  Removes a plugin from the CLI.

ALIASES
  $ flagsmith plugins unlink
  $ flagsmith plugins remove
```

## `flagsmith plugins update`

Update installed plugins.

```
USAGE
  $ flagsmith plugins update [-h] [-v]

FLAGS
  -h, --help     Show CLI help.
  -v, --verbose

DESCRIPTION
  Update installed plugins.
```
<!-- commandsstop -->
