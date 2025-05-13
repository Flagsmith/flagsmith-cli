import {Args, Command, Flags} from '@oclif/core'
import getAllFeatures from '../../util/get-all-features'
import * as fs from 'node:fs'

const DEFAULT_API = 'https://api.flagsmith.com'
let apiKey = ''

export default class FlagsmithGenerateTypes extends Command {
  static description = 'Fetch Flagsmith feature flags as JSON. By default pipes to stdout, allowing use with other tools; or write to a file with -o.'

  static examples = [
    `export FLAGSMITH_API_KEY=YOUR_KEY flagsmith generate-types PROJECT_ID | npx quicktype \\
      --src-lang json \\
      --lang typescript \\
      --just-types \\
      --explicit-unions \\
      --acronym-style camel \\
      --top-level FlagsmithTypes \\
      -o FlagsmithTypes.ts`,
    'export FLAGSMITH_API_KEY=YOUR_KEY flagsmith generate-types PROJECT_ID -o features.json',
  ]

  static flags = {
    api: Flags.string({
      char: 'a',
      description: 'The API endpoint (if self-hosted)',
      required: false,
      default: DEFAULT_API,
    }),
    output: Flags.string({
      char: 'o',
      description: 'Write the fetched JSON to a file instead of stdout',
      required: false,
    }),
    exclude: Flags.string({
      char: 'e',
      description: 'Comma-separated list of feature names to exclude',
      required: false,
    }),
    parseObjects: Flags.boolean({
      char: 'p',
      description: '(experimental) Include full object values when fetching features',
      required: false,
      default: false,
    }),
  }

  static args = {
    project: Args.string({
      description: 'Flagsmith project ID',
      required: true,
    }),
  }

  async run(): Promise<void> {
    const {args, flags} = await this.parse(FlagsmithGenerateTypes)

    if (!process.env.FLAGSMITH_API_KEY) {
      throw new Error('FLAGSMITH_API_KEY is not defined.')
    }

    apiKey = process.env.FLAGSMITH_API_KEY

    const options: {
      project: string
      apiKey: string
      api: string
      parseObjects: boolean
    } = {
      project: args.project,
      apiKey,
      api: flags.api,
      parseObjects: Boolean(flags.parseObjects),
    }

    if (flags.parseObjects) {
      this.warn('⚠️ Experimental: parsing full flag objects')
    }

    const features = await getAllFeatures(options)

    // Exclude specified flags
    const excludeList = flags.exclude?.split(',').map(name => name.trim())
    if (excludeList?.length) {
      for (const featureName of excludeList) {
        this.warn(`Excluding flag ${featureName}`)
        for (const envFeat of features) {
          delete envFeat[featureName]
        }
      }
    }

    const jsonString = JSON.stringify(features)

    if (flags.output) {
      this.warn(`Writing JSON to ${flags.output}`)
      fs.writeFileSync(flags.output, jsonString)
    } else {
      // Pipe JSON to stdout for further processing
      process.stdout.write(jsonString)
    }
  }
}
