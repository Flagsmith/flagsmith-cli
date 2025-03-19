import {createFlagsmithInstance} from "flagsmith/isomorphic";
import {IFlagsmithFeature} from "flagsmith/types";
import doGet from "./doGet";

export default async function (data:{apiKey:string, api: string, projectId: string }) {

  const get = doGet(data.api, data.apiKey)


  const environments: {results:{api_key:string, id:number}[]} = await get(`environments/?project=${data.projectId}`)
  return  await Promise.all(environments.results.map((v)=>{
    const instance = createFlagsmithInstance()
    return (
      // get a key value pair of each feature per environment
      instance.init({environmentID:v.api_key}).then(()=>{
        let features:Record<string, IFlagsmithFeature['value']>  = {}
        Object.keys(instance.getAllFlags()).map((key)=>{
          let value = instance.getValue(key)
          try {
            //attempt to parse the feature as json to get better types
            value = JSON.parse(instance.getValue(key))
          } catch (e) {}
          features[key] = value
        })
        return features
      })
    )
  }))
}
