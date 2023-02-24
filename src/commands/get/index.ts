import { Command, Flags } from '@oclif/core'
import fetch from 'node-fetch'
import flagsmith from 'flagsmith/isomorphic'
const fs = require('fs')
export default class FlagsmithGet extends Command {
  static description = 'Retrieve flagsmith features from the Flagsmith API and output them to a file.'

  static examples = [
    '$ FLAGSMITH_ENVIRONMENT=x flagsmith get',
    '$ flagsmith get <ENVIRONMENT_ID>',
    '$ flagsmith get --o ./my-file.json',
    '$ flagsmith get --a https://flagsmith.example.com/api/v1/',
    '$ flagsmith get --i flagsmith_identity',
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
  }

  static args = [
    {
      name: 'environment', description: 'The flagsmith environment key to use, defaults to the environment variable FLAGSMITH_ENVIRONMENT', required: false,
    },
  ]

  async run(): Promise<void> {
    const { args, flags } = await this.parse(FlagsmithGet)
    const environment = args.environment || process.env.FLAGSMITH_ENVIRONMENT
    const api = flags.api || process.env.FLAGSMITH_ENVIRONMENT
    if (!environment) {
      this.log('A flagsmith environment was not specified, run flagsmith get --help for more usage.')
    }
    const identity = flags.identity;
    let outputString = `Flagsmith: Retrieving flags from ${environment}`
    if (identity) {
      outputString += ` for identity ${identity}`
    }
    const output = flags.output
    outputString += `, outputing to ${output}.`
    this.log(outputString)

    await flagsmith.init({
      environmentID: environment,
      fetch: fetch,
      api: api,
      identity: identity,
    })
    fs.writeFileSync(output, JSON.stringify(flagsmith.getState()))
  }
}
