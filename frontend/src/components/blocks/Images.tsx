import type { ImageBlock } from '@/store/protocol'
import { imageSrc } from '@/lib/images'

export function Images({ blocks }: { blocks: ImageBlock[] }) {
  if (!blocks.length) return null
  return (
    <div className="mt-1.5 flex flex-wrap gap-1.5">
      {blocks.map((block, i) => (
        <a key={i} href={imageSrc(block)} target="_blank" rel="noreferrer">
          <img
            src={imageSrc(block)}
            alt=""
            className="max-h-44 rounded-[4px] border border-rule object-contain"
          />
        </a>
      ))}
    </div>
  )
}
