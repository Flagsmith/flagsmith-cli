import {Args, Command, Flags} from '@oclif/core'
import jsonToTypescript from "../../util/json-to-typescript";
import getAllFeatures from "../../util/get-all-features";

let api = `https://api.flagsmith.com`
let apiKey = ''

export default class FlagsmithGenerateTypes extends Command {
  static description = 'Generate TypeScript types from a Flagsmith Project'
  static examples = [
    "export FLAGSMITH_API_KEY=API_KEY flagsmith-generate-types PROJECT_ID",
    "export FLAGSMITH_API_KEY=API_KEY flagsmith-generate-types PROJECT_ID -a https://selfhosted-flagsmith.example.com",
    "export FLAGSMITH_API_KEY=API_KEY flagsmith-generate-types PROJECT_ID -o ./my-file.d.ts"
  ]

  static flags = {
    api: Flags.string({
      char: 'a', description: 'The API to use if you are self hosted', required: false, default: 'https://api.flagsmith.com',
    }),
  }

  static args = {
    project: Args.string({
      description:
        'The flagsmith project id to retrieve the features from',
    }),
    output: Args.string({
      description:
        'The flagsmith project id to retrieve the features from',
    }),
  }

  async run(): Promise<void> {
    const {args, flags} = await this.parse(FlagsmithGenerateTypes)

    if(!process.env.FLAGSMITH_API_KEY) {
      throw new Error("FLAGSMITH_API_KEY is not defined.")
    }
    apiKey = `${process.env.FLAGSMITH_API_KEY}`
    console.log(args.project)
    console.log(flags.api)
    console.log(apiKey)

    //Step 1: get all of the feature flags and environments for the project
    const allFeatures = await getAllFeatures({project:`${args.project}`, apiKey, api})
    // Example JSON string
    const jsonString = JSON.stringify(allFeatures)
    await jsonToTypescript(jsonString).then(ts => {
      console.log(ts)
    })
  }
}
