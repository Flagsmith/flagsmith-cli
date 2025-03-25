import {Args, Command, Flags} from '@oclif/core'
import jsonToTypescript from '../../util/json-to-typescript'
import getAllFeatures from '../../util/get-all-features'
import * as fs from 'node:fs'

const api = 'https://api.flagsmith.com'
let apiKey = ''

export default class FlagsmithGenerateTypes extends Command {
  static description = 'Generate TypeScript types from a Flagsmith Project'
  static examples = [
    'export FLAGSMITH_API_KEY=API_KEY flagsmith generate-types PROJECT_ID',
    'export FLAGSMITH_API_KEY=API_KEY flagsmith generate-types PROJECT_ID -a https://selfhosted-flagsmith.example.com',
    'export FLAGSMITH_API_KEY=API_KEY flagsmith generate-types PROJECT_ID -o ./my-file.d.ts',
    'export FLAGSMITH_API_KEY=API_KEY flagsmith generate-types PROJECT_ID -e feature_to_exclude',
  ]

  static flags = {
    api: Flags.string({
      char: 'a',
      description: 'The API to use if you are self hosted',
      required: false,
      default: 'https://api.flagsmith.com',
    }),
    output: Flags.string({
      char: 'o',
      description: 'The file path output',
      required: false,
      default: './flagsmith.d.ts',
    }),
    exclude: Flags.string({
      char: 'e',
      description: 'Comma separated list of feature names to exclude from type generation to test feature removal',
      required: false,
    }),
  }

  static args = {
    project: Args.string({
      description: 'The flagsmith project id to retrieve the features from',
      required: true,
    }),
    output: Args.string({
      description: 'The flagsmith project id to retrieve the features from',
      required: false,
    }),
  }

  async run(): Promise<void> {
    const {args, flags} = await this.parse(FlagsmithGenerateTypes)

    if (!process.env.FLAGSMITH_API_KEY) {
      throw new Error('FLAGSMITH_API_KEY is not defined.')
    }

    apiKey = process.env.FLAGSMITH_API_KEY

    const features = await getAllFeatures({project: `${args.project}`, apiKey, api})
    const excludeList = flags.exclude?.split(',').map(name => name.trim())
    if (excludeList?.length) {
      excludeList.map((feature: any) => {
        console.log('Excluding flag', feature)
        features.map(environmentFeature => {
          delete environmentFeature[feature]
        })
      })
    }

    const jsonString = JSON.stringify(features)
    const output = flags.output

    await jsonToTypescript(jsonString).then(ts => {
      console.log('Outputting types to', output)
      fs.writeFileSync(output, ts)
    })
  }
}
