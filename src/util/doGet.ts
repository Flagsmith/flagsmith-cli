import fetch from "node-fetch";

export default function (api:string, apiKey:string) {
  return (url: string) => fetch(`${api}/api/v1/${url}`, {
    headers: {
      'Content-Type': 'application/json',
      'AUTHORIZATION': `Token ${apiKey}`,
    }
  })
    .then((response) => {
      console.log(`${api}/v1/${url}`)
      console.log(response.status)
      // handle ajax requests
      if (response.status >= 200 && response.status < 300) {
        return Promise.resolve(response)
      }
      return Promise.reject(response)
    })
    .then((res) => res.json())

}
