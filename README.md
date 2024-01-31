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
flagsmith-cli/0.1.4 darwin-arm64 node-v18.16.0
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
  $ flagsmith get [ENVIRONMENT] [-o <value>] [-a <value>] [-i <value>] [-p] [-e flags|document]

ARGUMENTS
  ENVIRONMENT  The flagsmith environment key to use, defaults to the environment variable FLAGSMITH_ENVIRONMENT

FLAGS
  -a, --api=<value>       The API URL to fetch the feature flags from
  -e, --entity=<option>   [default: flags] The entity to fetch, this will either be the flags or an environment document
                          used for [local evaluation](https://docs.flagsmith.com/clients/server-side#local-evaluation-mo
                          de-network-behaviour).
                          <options: flags|document>
  -i, --identity=<value>  The identity for which to fetch feature flags
  -o, --output=<value>    [default: ./flagsmith.json] The file path output
  -p, --pretty            Prettify the output JSON

DESCRIPTION
  Retrieve flagsmith features from the Flagsmith API and output them to a file.

EXAMPLES
  $ flagsmith get <ENVIRONMENT_API_KEY>

  $ FLAGSMITH_ENVIRONMENT=x flagsmith get

  $ flagsmith get -e document

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
