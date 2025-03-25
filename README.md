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

```jsx
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
flagsmith-cli/0.1.2 darwin-arm64 node-v18.13.0
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

## `flagsmith get [ENVIRONMENT]`

Retrieve flagsmith features from the Flagsmith API and output them to a file.

```
USAGE
  $ flagsmith get [ENVIRONMENT] [-o <value>] [-a <value>] [-t
    <value> -i <value>] [-p] [-e flags|environment]

ARGUMENTS
  ENVIRONMENT  The flagsmith environment key to use, defaults to the environment
               variable FLAGSMITH_ENVIRONMENT

FLAGS
  -a, --api=<value>      [default: https://edge.api.flagsmith.com/api/v1/] The
                         API URL to fetch the feature flags from
  -e, --entity=<option>  [default: flags] The entity to fetch, this will either
                         be the flags or an environment document used for [local
                         evaluation](https://docs.flagsmith.com/clients/server-s
                         ide#local-evaluation-mode-network-behaviour).
                         <options: flags|environment>
  -o, --output=<value>   [default: ./flagsmith.json] The file path output
  -p, --pretty           Prettify the output JSON

IDENTITY FLAGS
  -i, --identity=<value>                    The identity for which to fetch
                                            feature flags
  -t, --trait=<trait_key>=<trait_value>...  Trait key-value pair, separated by
                                            an equals sign (=)

DESCRIPTION
  Retrieve flagsmith features from the Flagsmith API and output them to a file.

EXAMPLES
  $ flagsmith get <ENVIRONMENT_API_KEY>

  $ FLAGSMITH_ENVIRONMENT=abc123... flagsmith get

  $ FLAGSMITH_ENVIRONMENT=ser.abc123... flagsmith get -e environment

  $ flagsmith get -o ./my-file.json

  $ flagsmith get -a https://flagsmith.example.com/api/v1/

  $ flagsmith get -i flagsmith_identity

  $ flagsmith get -i flagsmith_identity -t my_trait_key=some_trait_value -t other_trait=other_value

  $ flagsmith get -p
```

_See code: [dist/commands/get/index.ts](https://github.com/Flagsmith/flagsmith-cli/blob/v0.1.4/dist/commands/get/index.ts)_

## `flagsmith generate-types [ENVIRONMENT]`

Generate a fully typed set of your project's current features. 

Note: This requires an API Key that is either generated for organisation settings or found in your account.

```
USAGE
  $ FLAGSMITH_API_KEY=<KEY> flagsmith generate-types [PROJECT_ID]

ARGUMENTS
  PROJECT_ID  The flagsmith project id you are fetching the types for.

FLAGS
  -a, --api=<value>      [default: https://edge.api.flagsmith.com/api/v1/] The
                         API URL to fetch the feature flags from
  -e, --exclude          [default: null] Exclude any feature you intend to remove, can be a csv e.g feature_a,feature_b.
  -o, --output=<value>   [default: ./flagsmith.d.ts] The file path output
  -p, --pretty           Prettify the output JSON

DESCRIPTION
  Retrieve type definitions for the features and their possible remote configuration values.
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

### Type generation examples

[1 - Adding a remote config.mov](1%20-%20Adding%20a%20remote%20config.mov)

[2 - Creating a new flag.mov](2%20-%20Creating%20a%20new%20flag.mov)

[3 - Dry run removing flag.mov](3%20-%20Dry%20run%20removing%20flag.mov)
