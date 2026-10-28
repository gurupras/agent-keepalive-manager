/// <reference types="vite/client" />

declare module 'virtual:icon-collections' {
  import type { IconifyJSON } from '@iconify/types'
  const collections: IconifyJSON[]
  export default collections
}
