import {Command, Flags} from '@oclif/core'
import fetch from 'node-fetch'
import flagsmith from 'flagsmith/isomorphic'
const fs = require('fs')
export default class FlagsmithGet extends Command {
  static description = 'Retrieve flagsmith features from the Flagsmith API and output them to a file.'

  static examples = [
    '$ flagsmith get <ENVIRONMENT_API_KEY>',
    '$ FLAGSMITH_ENVIRONMENT=x flagsmith get',
    '$ flagsmith get -e environment',
    '$ flagsmith get -o ./my-file.json',
    '$ flagsmith get -a https://flagsmith.example.com/api/v1/',
    '$ flagsmith get -i flagsmith_identity',
    '$ flagsmith get -p',
  ]

  static flags = {
    output: Flags.string({
      char: 'o', description: 'The file path output', required: false, default: './flagsmith.json',
    }),
    api: Flags.string({
      char: 'a', description: 'The API URL to fetch the feature flags from', required: false,
    }),
    identity: Flags.string({
      char: 'i', description: 'The identity for which to fetch feature flags', required: false,
    }),
    pretty: Flags.boolean({
      char: 'p', description: 'Prettify the output JSON', required: false, default: true,
    }),
    entity: Flags.string({
      options: ['flags', 'environment'],
      char: 'e',
      description: 'The entity to fetch, this will either be the flags or an environment document used for [local evaluation](https://docs.flagsmith.com/clients/server-side#local-evaluation-mode-network-behaviour).', required: false, default: 'flags',
    }),
  }

  static args = [
    {
      name: 'environment', description: 'The flagsmith environment key to use, defaults to the environment variable FLAGSMITH_ENVIRONMENT', required: false,
    },
  ]

  async run(): Promise<void> {
    const {args, flags} = await this.parse(FlagsmithGet)
    const environment = args.environment || process.env.FLAGSMITH_ENVIRONMENT
    const api = flags.api
    if (!environment) {
      this.log('A flagsmith environment was not specified, run flagsmith get --help for more usage.')
    }

    const identity = flags.identity
    let outputString = `Flagsmith: Retrieving flags from ${environment}`
    if (identity) {
      outputString += ` for identity ${identity}`
    }

    const output = flags.output
    const entity = flags.entity
    const isDocument = entity === 'environment'

    if (isDocument && !environment?.startsWith('ser.')) {
      this.error('In order to fetch the environment document you need to provide a server-side SDK token.')
      return
    }

    outputString += `, outputing to ${output}.`
    this.log(outputString)

    if (isDocument) {
      fetch(`${api || 'https://edge.api.flagsmith.com/api/v1/'}environment-document/flags/`, {
        headers: {
          'x-environment-key': environment,
        },
      }).then(res => res.json()).then(res => {
        if (flags.pretty) {
          fs.writeFileSync(output, JSON.stringify(res, null, 2))
        } else {
          fs.writeFileSync(output, JSON.stringify(res))
        }
      })
    } else {
      await flagsmith.init({
        environmentID: environment,
        fetch: fetch,
        api: api,
        identity: identity,
      })
      if (flags.pretty) {
        fs.writeFileSync(output, JSON.stringify(flagsmith.getState(), null, 2))
      } else {
        fs.writeFileSync(output, JSON.stringify(flagsmith.getState()))
      }
    }
  }
}
