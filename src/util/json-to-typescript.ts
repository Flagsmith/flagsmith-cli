import {quicktype, InputData, jsonInputForTargetLanguage}  from 'quicktype-core'

async function jsonToTypescript(jsonString:string) {
  const inputData = new InputData()
  const jsonInput = jsonInputForTargetLanguage('typescript')

  await jsonInput.addSource({
    name: 'FlagsmithTypes',
    samples: [jsonString],
  })

  inputData.addInput(jsonInput)

  const {lines} = await quicktype({
    inputData,
    lang: 'typescript',
    rendererOptions: {
      'just-types': 'true',
      'explicit-unions': 'true',
      'acronym-style': 'camel',
    },
  })

  return lines.join('\n')
}

export default jsonToTypescript
