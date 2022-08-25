<img width="100%" src="https://github.com/Flagsmith/flagsmith/raw/main/static-files/hero.png"/>

flagsmith-cli
=================

Retrieve Flagsmith state from an API and store it in a file.

This CLI can be used to bake default flags into your application as part of CI/CD, this provides support for offline applications and is also advised as part of our [Defensive Coding and Default Flags Documentation](https://docs.flagsmith.com/guides-and-examples/defensive-coding). An example of this can be seen [here](./example).


# Populating defaultFlags in your Project

The steps to using this to provide default flags are as follows.

- Install the cli ``npm i flagsmith-cli --save-dev``
- Call the cli as part of postinstall to create a flagsmith.json file:
```
"postinstall":"flagsmith get <ENV_API_KEY>"
 ```
- Call ``flagsmith.setState(json)`` prior to initialising the client and set defaultFlags to ``json.flags`` in ``flagsmith.init``

Example:


```typescript
import flagsmith from 'flagsmith'
import state from './flagsmith.json'

flagsmith.setState(state);
flagsmith.init({defaultFlags: state.flags})
```


<!-- tocstop -->
# Usage - Global
<!-- usage -->
```sh-session
$ npm install -g flagsmith-cli
$ flagsmith COMMAND
...
```
# Usage - Locally
<!-- usage -->
```sh-session
$ npm i flagsmith-cli --save
$ npx flagsmith COMMAND
...
```
<!-- usagestop -->
# Commands
<!-- commands -->
* [`flagsmith get [ENVIRONMENT]`](#flagsmith-get-environment)
* [`flagsmith help [COMMAND]`](#flagsmith-help-command)

## `flagsmith get [ENVIRONMENT]`

Retrieve flagsmith features from the Flagsmith API and output them to a file.

```
USAGE
  $ flagsmith get [ENVIRONMENT] [-o <value>] [-a <value>]

ARGUMENTS
  ENVIRONMENT  The flagsmith environment key to use, defaults to the environment variable FLAGSMITH_ENVIRONMENT

FLAGS
  -a, --api=<value>     The API URL to fetch the feature flags from
  -o, --output=<value>  [default: ./flagsmith.json] The file path output

DESCRIPTION
  Retrieve flagsmith features from the Flagsmith API and output them to a file.

EXAMPLES
  $ FLAGSMITH_ENVIRONMENT=x flagsmith get

  $ flagsmith get <ENVIRONMENT_ID>

  $ flagsmith get --o ./my-file.json

  $ flagsmith get --a https://flagsmith.example.com/api/v1/
```

_See code: [dist/commands/get/index.ts](https://github.com/Flagsmith/flagsmith-cli/blob/v0.1.0/dist/commands/get/index.ts)_

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

