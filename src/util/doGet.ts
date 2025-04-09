import fetch from 'node-fetch'

function isMasterAPIKey(apiKey:string) {
  return apiKey.includes('.')
}

export default function (api:string, apiKey:string) {
  return (url: string) => {

    return fetch(`${new URL(`/api/v1/${url}`, api)}`, {
      headers: {
        'Content-Type': 'application/json',
        AUTHORIZATION: isMasterAPIKey(apiKey) ? `Authorization Api-Key ${apiKey}` : `Token ${apiKey}`,
      },
    })
    .then(response => {
      // handle ajax requests
      if (response.status >= 200 && response.status < 300) {
        return Promise.resolve(response)
      }

      return Promise.reject(response)
    })
    .then(res => res.json())
  }
}
