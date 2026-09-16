import { LocalizedError } from '@/i18n/core'
/** Pasted and dropped files on their way into a prompt. The hard limits belong to the agent's
 *  descriptor and are enforced by the server; these are only what is worth sending at all. */
import type { ImageBlock, ImageMediaType } from '@/store/protocol'

export const IMAGE_MEDIA_TYPES = ['image/png', 'image/jpeg', 'image/gif', 'image/webp'] as const
const MAX_BASE64 = 8_000_000
/** The vision API downscales anything larger before the model sees it, so doing it here is free. */
const MAX_EDGE = 1568
/** Past this a lossless copy is not worth the bytes; the image is re-encoded as JPEG. */
const PNG_BUDGET = 1_500_000

export interface Attachment {
  id: string
  media_type: ImageMediaType
  /** Bare base64, no `data:` prefix — the shape `PromptImage` expects. */
  data: string
  name: string
}

const supported = (t: string): t is ImageMediaType => (IMAGE_MEDIA_TYPES as readonly string[]).includes(t)

/** `src` for a protocol image block. */
export const imageSrc = (block: ImageBlock) => `data:${block.media_type};base64,${block.data_base64}`

/** The block an attachment becomes on its way into a prompt. */
export const toBlock = (a: Attachment): ImageBlock => ({ type: 'image', media_type: a.media_type, data_base64: a.data })

export const attachmentSrc = (a: Attachment) => `data:${a.media_type};base64,${a.data}`

/** The image files in a paste or a drop, in the order they were given. */
export function imageFiles(source: DataTransfer | FileList | null): File[] {
  if (!source) return []
  const files = source instanceof DataTransfer ? Array.from(source.files) : Array.from(source)
  return files.filter((f) => f.type.startsWith('image/'))
}

function encode(canvas: HTMLCanvasElement, type: ImageMediaType, quality?: number): Promise<Blob> {
  return new Promise((resolve, reject) => {
    canvas.toBlob((b) => (b ? resolve(b) : reject(new LocalizedError('error.imageEncode'))), type, quality)
  })
}

function render(bitmap: ImageBitmap, w: number, h: number, type: ImageMediaType): Promise<Blob> {
  const canvas = document.createElement('canvas')
  canvas.width = w
  canvas.height = h
  const ctx = canvas.getContext('2d')
  if (!ctx) throw new LocalizedError('error.canvas')
  // JPEG has no alpha channel: without this, transparent pixels come out black.
  if (type === 'image/jpeg') {
    ctx.fillStyle = '#ffffff'
    ctx.fillRect(0, 0, w, h)
  }
  ctx.drawImage(bitmap, 0, 0, w, h)
  return encode(canvas, type, type === 'image/jpeg' ? 0.85 : undefined)
}

/** Downscale to something the model can actually use, and land on a format it accepts. */
async function normalize(file: File): Promise<{ media_type: ImageMediaType; blob: Blob }> {
  // The one format a canvas round-trip would damage: it would keep only the first frame.
  if (file.type === 'image/gif') return { media_type: 'image/gif', blob: file }
  const bitmap = await createImageBitmap(file)
  try {
    const scale = Math.min(1, MAX_EDGE / Math.max(bitmap.width, bitmap.height))
    if (scale === 1 && supported(file.type) && file.size <= PNG_BUDGET) return { media_type: file.type, blob: file }
    const w = Math.max(1, Math.round(bitmap.width * scale))
    const h = Math.max(1, Math.round(bitmap.height * scale))
    // Photos are already lossy, so re-encoding them as PNG would only inflate them.
    if (file.type === 'image/jpeg') return { media_type: 'image/jpeg', blob: await render(bitmap, w, h, 'image/jpeg') }
    const png = await render(bitmap, w, h, 'image/png')
    if (png.size <= PNG_BUDGET) return { media_type: 'image/png', blob: png }
    return { media_type: 'image/jpeg', blob: await render(bitmap, w, h, 'image/jpeg') }
  } finally {
    bitmap.close()
  }
}

function toBase64(blob: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader()
    reader.onerror = () => reject(new LocalizedError('imageReadFailed'))
    reader.onload = () => resolve(String(reader.result).split(',')[1] ?? '')
    reader.readAsDataURL(blob)
  })
}

let seq = 0

export async function toAttachment(file: File): Promise<Attachment> {
  const name = file.name
  let normalized: { media_type: ImageMediaType; blob: Blob }
  try {
    normalized = await normalize(file)
  } catch {
    throw new LocalizedError(name ? 'error.imageRead' : 'imageReadFailed', { name })
  }
  const data = await toBase64(normalized.blob)
  if (!data) throw new LocalizedError(name ? 'error.imageRead' : 'imageReadFailed', { name })
  if (data.length > MAX_BASE64) throw new LocalizedError(name ? 'error.imageLarge' : 'error.image_too_large', { name })
  return { id: `img-${Date.now()}-${seq++}`, media_type: normalized.media_type, data, name }
}
