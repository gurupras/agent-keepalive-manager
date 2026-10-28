import { ref } from 'vue'

/** now is a shared clock (epoch ms) that ticks every second. */
export const now = ref(Date.now())

let timer: ReturnType<typeof setInterval> | undefined = setInterval(() => { now.value = Date.now() }, 1000)

/** freezeNow stops the clock at ms (Storybook, tests). */
export function freezeNow (ms: number) {
  clearInterval(timer)
  timer = undefined
  now.value = ms
}
