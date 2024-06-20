<img width="100%" src="https://github.com/Flagsmith/flagsmith/raw/main/static-files/hero.png"/>

flagsmith-cli
=================

Retrieve Flagsmith state from an API and store it in a file.

This CLI can be used to bake default flags into your application as part of CI/CD, this provides support for offline applications and is also advised as part of our [Defensive Coding and Default Flags Documentation](https://docs.flagsmith.com/guides-and-examples/defensive-coding). An example of this can be seen [here](./example).


# Populating defaultFlags in your Project

The steps to using this to provide default flags are as follows. An example of this can be found [here](./example). The main steps to achieving this are as follows:


1. Install the cli ``npm i flagsmith-cli --save-dev``
2. Call the cli as part of postinstall to create a ``flagsmith.json`` file:

```export FLAGSMITH_ENVIRONMENT=API_KEY```

```
"postinstall": "flagsmith get"
```
An example of this can be seen [here](./example/src/index.tsx). 
3. In your application, import the outputted JSON and initialise the client with the json ``flagsmith.init({state:json, environmentID: json.environmentID})``

**Example:**

```typescript
import flagsmith from 'flagsmith'
import state from './flagsmith.json'

flagsmith.init({state, environmentID: state.environmentID})
```

**Example with React:**

```typescript
import state from './flagsmith.json'
ReactDOM.render(
  <FlagsmithProvider options={{environmentID: state.environmentID, state}} flagsmith={flagsmith}>
    <App />
  </FlagsmithProvider>,
  document.getElementById('root')
);
```

<!-- tocstop -->
# Usage - Global
<!-- usage -->
```sh-session
$ npm install -g flagsmith-cli
$ flagsmith COMMAND
running command...
$ flagsmith (--version)
flagsmith-cli/0.2.0 darwin-arm64 node-v21.6.1
$ flagsmith --help [COMMAND]
USAGE
  $ flagsmith COMMAND
...
```
<!-- usagestop -->
# Commands
<!-- commands -->
* [`flagsmith get [ENVIRONMENT]`](#flagsmith-get-environment)
* [`flagsmith help [COMMANDS]`](#flagsmith-help-commands)
* [`flagsmith plugins`](#flagsmith-plugins)
* [`flagsmith plugins:install PLUGIN...`](#flagsmith-pluginsinstall-plugin)
* [`flagsmith plugins:inspect PLUGIN...`](#flagsmith-pluginsinspect-plugin)
* [`flagsmith plugins:install PLUGIN...`](#flagsmith-pluginsinstall-plugin-1)
* [`flagsmith plugins:link PLUGIN`](#flagsmith-pluginslink-plugin)
* [`flagsmith plugins:uninstall PLUGIN...`](#flagsmith-pluginsuninstall-plugin)
* [`flagsmith plugins:uninstall PLUGIN...`](#flagsmith-pluginsuninstall-plugin-1)
* [`flagsmith plugins:uninstall PLUGIN...`](#flagsmith-pluginsuninstall-plugin-2)
* [`flagsmith plugins update`](#flagsmith-plugins-update)

## `flagsmith get [ENVIRONMENT]`

Retrieve flagsmith features from the Flagsmith API and output them to a file.

```
USAGE
  $ flagsmith get [ENVIRONMENT] [-o <value>] [-a <value>] [-i <value>] [-p] [-e flags|environment]

ARGUMENTS
  ENVIRONMENT  The flagsmith environment key to use, defaults to the environment variable FLAGSMITH_ENVIRONMENT

FLAGS
  -a, --api=<value>       The API URL to fetch the feature flags from
  -e, --entity=<option>   [default: flags] The entity to fetch, this will either be the flags or an environment document
                          used for [local evaluation](https://docs.flagsmith.com/clients/server-side#local-evaluation-mo
                          de-network-behaviour).
                          <options: flags|environment>
  -i, --identity=<value>  The identity for which to fetch feature flags
  -o, --output=<value>    [default: ./flagsmith.json] The file path output
  -p, --pretty            Prettify the output JSON

DESCRIPTION
  Retrieve flagsmith features from the Flagsmith API and output them to a file.

EXAMPLES
  $ flagsmith get <ENVIRONMENT_API_KEY>

  $ FLAGSMITH_ENVIRONMENT=x flagsmith get

  $ flagsmith get -e environment

  $ flagsmith get -o ./my-file.json

  $ flagsmith get -a https://flagsmith.example.com/api/v1/

  $ flagsmith get -i flagsmith_identity

  $ flagsmith get -p
```

_See code: [dist/commands/get/index.ts](https://github.com/Flagsmith/flagsmith-cli/blob/v0.2.0/dist/commands/get/index.ts)_

## `flagsmith help [COMMANDS]`

Display help for flagsmith.

```
USAGE
  $ flagsmith help [COMMANDS] [-n]

ARGUMENTS
  COMMANDS  Command to show help for.

FLAGS
  -n, --nested-commands  Include all nested commands in the output.

DESCRIPTION
  Display help for flagsmith.
```

_See code: [@oclif/plugin-help](https://github.com/oclif/plugin-help/blob/v5.2.5/src/commands/help.ts)_

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

_See code: [@oclif/plugin-plugins](https://github.com/oclif/plugin-plugins/blob/v2.3.2/src/commands/plugins/index.ts)_

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

GLOBAL FLAGS
  --json  Format output as json.

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
* [`flagsmith get [ENVIRONMENT]`](#flagsmith-get-environment)
* [`flagsmith help [COMMANDS]`](#flagsmith-help-commands)

## `flagsmith get [ENVIRONMENT]`

Retrieve flagsmith features from the Flagsmith API and output them to a file.

```
USAGE
  $ flagsmith get [ENVIRONMENT] [-o <value>] [-a <value>] [-i <value>] [-p] [-e flags|environment]

ARGUMENTS
  ENVIRONMENT  The flagsmith environment key to use, defaults to the environment variable FLAGSMITH_ENVIRONMENT

FLAGS
  -a, --api=<value>       The API URL to fetch the feature flags from
  -e, --entity=<option>   [default: flags] The entity to fetch, this will either be the flags or an environment document
                          used for [local evaluation](https://docs.flagsmith.com/clients/server-side#local-evaluation-mo
                          de-network-behaviour).
                          <options: flags|environment>
  -i, --identity=<value>  The identity for which to fetch feature flags
  -o, --output=<value>    [default: ./flagsmith.json] The file path output
  -p, --pretty            Prettify the output JSON

DESCRIPTION
  Retrieve flagsmith features from the Flagsmith API and output them to a file.

EXAMPLES
  $ flagsmith get <ENVIRONMENT_API_KEY>

  $ FLAGSMITH_ENVIRONMENT=x flagsmith get

  $ flagsmith get -e environment

  $ flagsmith get -o ./my-file.json

  $ flagsmith get -a https://flagsmith.example.com/api/v1/

  $ flagsmith get -i flagsmith_identity

  $ flagsmith get -p
```

_See code: [dist/commands/get/index.ts](https://github.com/Flagsmith/flagsmith-cli/blob/v0.1.4/dist/commands/get/index.ts)_

## `flagsmith help [COMMANDS]`

Display help for flagsmith.

```
USAGE
  $ flagsmith help [COMMANDS] [-n]

ARGUMENTS
  COMMANDS  Command to show help for.

FLAGS
  -n, --nested-commands  Include all nested commands in the output.

DESCRIPTION
  Display help for flagsmith.
```
