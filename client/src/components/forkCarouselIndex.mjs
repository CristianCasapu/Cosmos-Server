// Fork addition: the slide shown after moving by `step` (±1) from `index` among
// `count` slides, wrapping at both ends (components/forkCarousel.jsx).
export const nextIndex = (index, step, count) => {
  if (!Number.isInteger(count) || count < 1) return 0;
  const from = Number.isInteger(index) ? index : 0;
  return (((from + step) % count) + count) % count;
};
