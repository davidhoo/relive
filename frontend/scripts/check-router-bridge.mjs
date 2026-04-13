import fs from 'node:fs'

const bridgePath = 'src/router/bridge.ts'
const requestPath = 'src/utils/request.ts'
const mainPath = 'src/main.ts'

if (!fs.existsSync(bridgePath)) {
  throw new Error('router bridge file missing')
}

const request = fs.readFileSync(requestPath, 'utf8')
const main = fs.readFileSync(mainPath, 'utf8')

if (request.includes("import('@/router')")) {
  throw new Error('request.ts still dynamically imports router')
}

if (!main.includes('registerRouter(')) {
  throw new Error('main.ts does not register router bridge')
}
