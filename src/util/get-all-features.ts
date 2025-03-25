import fetch from 'node-fetch'
import {createFlagsmithInstance} from 'flagsmith/isomorphic'
import {IFlagsmithFeature} from 'flagsmith'
import doGet from './doGet'

export default async function (data: { apiKey: string, api: string, project: string }) {
  const {api, apiKey, project} = data
  const getReq = doGet(api, apiKey)
  const environments: { api_key: string, id: number }[] = await getReq(`projects/${project}/environments/`)
  return Promise.all(environments.map(v => {
    const instance = createFlagsmithInstance()
    return instance.init({environmentID: v.api_key, fetch}).then(() => {
      const features: Record<string, IFlagsmithFeature['value']> = {}
      for (const key of Object.keys(instance.getAllFlags())) {
        let value = instance.getValue(key)
        try {
          value = JSON.parse(instance.getValue(key))
        } catch {}

        features[key] = value
      }

      const sortedFeatures = Object.keys(features)
      .sort()
      .reduce((acc, key) => ({...acc, [key]: features[key]}), {} as Record<string, IFlagsmithFeature['value']>)
      return sortedFeatures
    })
  }))
}
