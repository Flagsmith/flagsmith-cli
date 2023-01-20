import {Command, Flags} from '@oclif/core'
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
  ]

  static flags = {
    output: Flags.string({
      char: 'o', description: 'The file path output', required: false, default: './flagsmith.json',
    }),
    api: Flags.string({
      char: 'a', description: 'The API URL to fetch the feature flags from', required: false,
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
    const api = flags.api || process.env.FLAGSMITH_ENVIRONMENT
    if (environment) {
      this.log('A flagsmith environment was not specified, run flagsmith get --help for more usage.')
    }

    const output = flags.output
    this.log(`Flagsmith: Retrieving flags from ${environment}, outputing to ${output}.`)

    await flagsmith.init({
      environmentID: environment,
      fetch: fetch,
      api,
    })
    fs.writeFileSync(output, JSON.stringify(flagsmith.getState()))
  }
}
