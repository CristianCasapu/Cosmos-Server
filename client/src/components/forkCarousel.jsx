// Fork addition: the Market's slide shows (showcase and screenshots), in place of
// react-material-ui-carousel. That library is no longer maintained: it requires
// MUI 5 and React 18 and brought its own copy of both plus an old framer-motion.
// Same behaviour: one slide at a time, turning by itself every few seconds
// (paused while the pointer is over it or a control has the focus), arrows that
// show on hover or focus, dots under the slides. Colours come from the theme.
// A slide that is not shown is hidden from the keyboard and screen readers.
import * as React from 'react';
import { Box, IconButton, Tooltip } from '@mui/material';
import { LeftOutlined, RightOutlined } from '@ant-design/icons';
import { useTranslation } from 'react-i18next';
import { nextIndex } from './forkCarouselIndex.mjs';

const DURATION = 500;

const ForkCarousel = ({ children, animation = 'slide', autoPlay = true, interval = 4000, className, label }) => {
  const { t } = useTranslation();
  const slides = React.Children.toArray(children).filter(Boolean);
  const count = slides.length;
  const [index, setIndex] = React.useState(0);
  const [paused, setPaused] = React.useState(false);
  const current = count ? Math.min(index, count - 1) : 0;

  React.useEffect(() => {
    if (!autoPlay || paused || count < 2) return undefined;
    const timer = setInterval(() => setIndex((i) => nextIndex(i, 1, count)), interval);
    return () => clearInterval(timer);
  }, [autoPlay, paused, count, interval]);

  if (!count) return null;
  const go = (step) => setIndex((i) => nextIndex(i, step, count));
  const slide = animation !== 'fade';

  const arrow = (side) => (
    <Tooltip title={side === 'prev' ? t('navigation.market.carouselPrevious', 'Previous') : t('navigation.market.carouselNext', 'Next')}>
      <IconButton
        className="ct-carousel-arrow"
        aria-label={side === 'prev' ? t('navigation.market.carouselPrevious', 'Previous') : t('navigation.market.carouselNext', 'Next')}
        onClick={() => go(side === 'prev' ? -1 : 1)}
        sx={{
          position: 'absolute',
          top: '50%',
          transform: 'translateY(-50%)',
          [side === 'prev' ? 'left' : 'right']: 10,
          zIndex: 1,
          bgcolor: 'background.paper',
          color: 'text.primary',
          boxShadow: 1,
          opacity: 0,
          transition: 'opacity 200ms',
          '&:hover': { bgcolor: 'background.paper' },
          '&:focus-visible': { opacity: 1 },
        }}
      >
        {side === 'prev' ? <LeftOutlined /> : <RightOutlined />}
      </IconButton>
    </Tooltip>
  );

  return (
    <Box
      className={className}
      role="region"
      aria-roledescription="carousel"
      aria-label={label}
      onMouseEnter={() => setPaused(true)}
      onMouseLeave={() => setPaused(false)}
      onFocus={() => setPaused(true)}
      onBlur={() => setPaused(false)}
      sx={{ position: 'relative', '&:hover .ct-carousel-arrow': { opacity: 0.85 } }}
    >
      <Box sx={{ position: 'relative', overflow: 'hidden' }}>
        <Box sx={slide ? {
          display: 'flex',
          transform: `translateX(-${current * 100}%)`,
          transition: `transform ${DURATION}ms ease-in-out`,
        } : { display: 'grid' }}>
          {slides.map((child, i) => {
            const shown = i === current;
            return (
              <Box
                key={i}
                role="group"
                aria-roledescription="slide"
                aria-hidden={!shown}
                sx={{
                  flex: '0 0 100%',
                  minWidth: 0,
                  ...(slide ? {} : { gridArea: '1 / 1', opacity: shown ? 1 : 0, transition: `opacity ${DURATION}ms` }),
                  // out of the tab order and the accessibility tree once it has moved away
                  visibility: shown ? 'visible' : 'hidden',
                  transition: slide
                    ? `visibility 0s linear ${shown ? 0 : DURATION}ms`
                    : `opacity ${DURATION}ms, visibility 0s linear ${shown ? 0 : DURATION}ms`,
                }}
              >
                {child}
              </Box>
            );
          })}
        </Box>
        {count > 1 && arrow('prev')}
        {count > 1 && arrow('next')}
      </Box>
      {count > 1 && (
        <Box sx={{ mt: '10px', display: 'flex', justifyContent: 'center', gap: '4px' }}>
          {slides.map((_, i) => (
            <Box
              key={i}
              component="button"
              type="button"
              aria-label={t('navigation.market.carouselGoTo', 'Show slide {{n}}', { n: i + 1 })}
              aria-current={i === current ? 'true' : undefined}
              onClick={() => setIndex(i)}
              sx={{
                width: 15, height: 15, p: 0, border: 0, borderRadius: '50%', cursor: 'pointer',
                bgcolor: i === current ? 'text.primary' : 'text.disabled',
                opacity: i === current ? 0.8 : 0.5,
                transition: 'opacity 200ms, background-color 200ms',
                '&:hover': { opacity: 0.9 },
                '&:focus-visible': { outline: '2px solid', outlineColor: 'primary.main', outlineOffset: 2 },
              }}
            />
          ))}
        </Box>
      )}
    </Box>
  );
};

export default ForkCarousel;
