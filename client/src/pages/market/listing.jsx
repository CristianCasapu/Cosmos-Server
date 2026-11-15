import { Box, CircularProgress, Input, InputAdornment, Stack, Tooltip, Alert, Typography } from "@mui/material"; // Fork addition: Typography
import { HomeBackground, TransparentHeader } from "../home";
import { useEffect, useState } from "react";
import * as API from "../../api";
import { useTheme } from "@emotion/react";
import Grid2 from '@mui/material/Grid';
import { useParams } from "react-router";
import Carousel from '../../components/forkCarousel'; // Fork addition: replaces react-material-ui-carousel (unmaintained, MUI 5 only)
import { Paper, Button, Chip } from '@mui/material'
import { Link } from "react-router";
import { Link as LinkMUI } from '@mui/material'
import DockerComposeImport from '../servapps/containers/docker-compose';
import { AppstoreAddOutlined, SearchOutlined, WarningOutlined } from "@ant-design/icons";
import ResponsiveButton from "../../components/responseiveButton";
import { useClientInfos } from "../../utils/hooks";
import { PERM_RESOURCES } from "../../utils/permissions";
import PermissionGuard from "../../components/permissionGuard";
import EditSourcesModal from "./sources";
import { PersistentCheckbox } from "../../components/persistentInput";
import { useTranslation } from 'react-i18next'; 
import DOMPurify from 'dompurify';
import { marketArchFits } from '../../utils/dataPlaces'; // Fork addition

function Screenshots({ screenshots }) {
  const { t } = useTranslation(); // Fork addition: the slide show's name
  const aspectRatioContainerStyle = {
    position: 'relative',
    overflow: 'hidden',
    paddingTop: '56.25%', // 9 / 16 = 0.5625 or 56.25%
    height: 0,
  };

  // This will position the image correctly within the aspect ratio container
  const imageStyle = {
    position: 'absolute',
    top: 0,
    left: 0,
    bottom: 0,
    right: 0,
    width: '100%',
    height: '100%',
    objectFit: 'cover', // This will cover the area without losing the aspect ratio
  };

  // Fork addition: on a phone the frame is as tall as the picture (a fixed 400 px left a band of empty space under it)
  const frame = { height: { xs: 'auto', sm: '400px' } };
  return screenshots.length > 1 ? (
    <Carousel animation="slide" label={t('navigation.market.screenshots', 'Screenshots')}>
      {
        screenshots.map((item, i) => <Box sx={frame} key={i}>
          <img style={{ maxHeight: '100%', width: '100%', objectFit: 'contain' }} src={item} alt="" />
        </Box>)
      }
    </Carousel>)
    : <Box sx={frame}>
        <img src={screenshots[0]} style={{ maxHeight: '100%', width: '100%', objectFit: 'contain' }} alt="" />
      </Box>
}

function Showcases({ showcase, isDark, isAdmin }) {
  const { t } = useTranslation(); // Fork addition: the slide show's name
  // Fork addition: the slides stay inside their frame (on a phone two half slides showed), and with
  // "reduce motion" on the device the showcase does not turn by itself and changes without sliding
  const still = typeof window !== 'undefined' && window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)').matches;
  return (
    <Carousel className="ct-market-showcase" animation={still ? 'fade' : 'slide'} autoPlay={!still} label={t('navigation.market.featured', 'Featured apps')}>
      {
        showcase.map((item, i) => <ShowcasesItem isDark={isDark} key={i} item={item} isAdmin={isAdmin} />)
      }
    </Carousel>
  )
}

function ShowcasesItem({ isDark, item, isAdmin }) {
  const { t, i18n } = useTranslation();
  // Fork addition: on a phone the screenshot is a band above the text instead of under it (the text panel covered it);
  // the description is cut after a few lines instead of in the middle of one; "View" is a link styled as a button (one Tab stop)
  return (
    <Paper sx={{
      position: 'relative',
      background: { xs: 'none', sm: 'url(' + item.screenshots[0] + ')' },
      height: { xs: 'auto', sm: '31vh' },
      backgroundSize: 'auto 100%',
      maxWidth: '120vh',
      margin: 'auto',
      overflow: 'hidden',
    }}>
      <Box className="ct-showcase-shot" aria-hidden="true" sx={{
        display: { xs: 'block', sm: 'none' },
        height: '140px',
        backgroundImage: 'url(' + item.screenshots[0] + ')',
        backgroundSize: 'cover',
        backgroundPosition: 'center top',
      }} />
      <Stack direction="row" spacing={2} style={{ height: '100%', overflow: 'hidden' }} sx={{ justifyContent: 'flex-end' }}>
        <Stack direction="column" spacing={2} style={{ height: '100%' }} sx={{
          backgroundColor: isDark ? 'var(--ct-surface, #20232b)' : 'rgba(255,255,255)',
          padding: '20px 100px',
          width: '50%',
          filter: 'drop-shadow(-20px 0px 20px rgba(0, 0, 0, 1))',

          '@media (max-width: 1100px)': {
            width: '70%',
            padding: '20px 40px',
          },

          '@media (max-width: 600px)': {
            width: '100%',
            padding: '16px',
            filter: 'none',
          }
        }}>
          <Stack direction="row" spacing={2} sx={{ alignItems: 'center' }}>
            <img src={item.icon} alt="" style={{ width: '36px', height: '36px' }} />
            <h2 style={{ margin: 0 }}>{item.name}</h2>
          </Stack>
          <Box component="p" className="ct-showcase-text" dangerouslySetInnerHTML={{ __html: DOMPurify.sanitize(item?.translation?.[i18n?.resolvedLanguage]?.longDescription || item?.translation?.[i18n?.resolvedLanguage.substr?.(0,2)]?.longDescription || item.longDescription) }} sx={{
            overflow: 'hidden',
            margin: 0,
            display: '-webkit-box',
            WebkitBoxOrient: 'vertical',
            WebkitLineClamp: { xs: 3, sm: 4 },
          }}></Box>
          <Stack direction="row" spacing={2} sx={{ justifyContent: 'flex-start', flexWrap: 'wrap' }} useFlexGap>
            <DockerComposeImport installerInit defaultName={item.name} dockerComposeInit={item.compose} />
            <Button component={Link} to={"/cosmos-ui/market-listing/cosmos-cloud/" + item.name} className="CheckButton" color="primary" variant="outlined">
              {t('navigation.market.viewButton')}
            </Button>
          </Stack>
        </Stack>
      </Stack>
    </Paper>
  )
}

const appCardStyle = (theme) => ({
  width: '100%',
  backgroundColor: theme.palette.mode === 'dark' ? 'var(--ct-surface, #20232b)' : 'rgba(255,255,255)',
  ...theme.typography.body2,
  padding: theme.spacing(1),
  color: theme.palette.text.secondary,
})

const gridAnim = {
  transition: 'all 0.2s ease',
  opacity: 1,
  transform: 'translateY(0px)',
  '&.MuiGrid2-item--hidden': {
    opacity: 0,
    transform: 'translateY(-20px)',
  },
};

const MarketPage = () => {
  const { t, i18n } = useTranslation();
  const [apps, setApps] = useState([]);
  const [showcase, setShowcase] = useState([]);
  const theme = useTheme();
  const isDark = theme.palette.mode === 'dark';
  const { appName, appStore } = useParams();
  const [search, setSearch] = useState("");
  const [filterDups, setFilterDups] = useState(false);
  const { hasPermission } = useClientInfos();
  const isAdmin = hasPermission(PERM_RESOURCES);

  // Fork addition: the skin's colours where it is loaded (upstream's neutral greys were a slab on the skin's navy)
  const backgroundStyle = isDark ? {
    backgroundColor: 'var(--ct-bg, #141414)',
  } : {
    backgroundColor: 'var(--ct-bg, rgb(245,245,247))',
  };

  const [loaded, setLoaded] = useState(false); // Fork addition: the spinners only until the answer came
  const refresh = () => {
    API.market.list().then((res) => {
      setApps(res.data.all);
      setShowcase(res.data.showcase);
    }).finally(() => setLoaded(true));
  };

  useEffect(() => {
    refresh();
  }, []);

  let openedApp = null;
  if (appName && Object.keys(apps).length > 0) {
    // Fork addition: an app or a source that is not in the list (old link, removed source) crashed the page
    openedApp = (apps[appStore] || []).find((app) => app.name === appName) || null;
    if (openedApp) openedApp.appstore = appStore;
  }

  let appList = apps && Object.keys(apps).reduce((acc, appstore) => {
    const a = apps[appstore].map((app) => {
      app.appstore = appstore;
      return app;
    });

    return acc.concat(a);
  }, []);

  appList.sort((a, b) => {
    if (a.name > b.name) {
      return 1;
    }

    if (a.name < b.name) {
      return -1;
    }

    if (a.appstore > b.appstore || b.appstore === 'cosmos-cloud') {
      return 1;
    }

    if (a.appstore < b.appstore || a.appstore === 'cosmos-cloud') {
      return -1;
    }

    return 0;
  });

  let filteredAppList = appList.filter((app) => {
    if (!search || search.length <= 2) {
      return true;
    }
    return app.name.toLowerCase().includes(search.toLowerCase()) ||
      app.tags.join(' ').toLowerCase().includes(search.toLowerCase());
  })
  .filter((app) => {
    if (!filterDups) {
      return true;
    } else if(app.appstore === 'cosmos-cloud') {
      return true;
    } else if(appList.filter((a) => a.name === app.name && a.appstore === 'cosmos-cloud').length > 0) {
      return false;
    }

    return true;
  });

  return <>
    <HomeBackground />
    <TransparentHeader />
    {openedApp && <Box style={{
      position: 'fixed',
      top: 0,
      left: 0,
      width: '100%',
      height: '100%',
      zIndex: 1300,
      backgroundColor: 'rgba(0,0,0,0.5)',
    }}>
      <Link to="/cosmos-ui/market-listing" as={Box} tabIndex={-1} aria-hidden="true" /* Fork addition: the backdrop closes on a click; "Close" is the control */
        style={{
          position: 'fixed',
          top: 0,
          left: 0,
          width: '100%',
          height: '100%',
        }}></Link>

      <Stack direction="row" spacing={2} style={{ height: '100%' }} sx={{ justifyContent: 'flex-end' }}>
        <Stack direction="column" spacing={3} style={{ height: '100%', overflow: "auto" }} sx={{
          backgroundColor: isDark ? 'var(--ct-surface, #20232b)' : 'rgba(255,255,255)',
          padding: '80px 80px',
          width: '100%',
          maxWidth: '800px',
          filter: 'drop-shadow(-20px 0px 20px rgba(0, 0, 0, 1))',

          '@media (max-width: 700px)': {
            padding: '60px 40px',
          },

          '@media (max-width: 500px)': {
            padding: '40px 20px',
          }
        }}>

          {/* Fork addition: a link styled as a button (one Tab stop; it was a button inside a link) */}
          <Button component={Link} to="/cosmos-ui/market-listing" className="CheckButton" color="primary" variant="outlined" sx={{ alignSelf: 'flex-start' }}>
            {t('global.close')}
          </Button>

          <div style={{ textAlign: 'center' }}>
            <Screenshots screenshots={openedApp.screenshots} isAdmin={isAdmin}/>
          </div>

          <Stack direction="row" spacing={2}>
            <img src={openedApp.icon} style={{ width: '36px', height: '36px' }} />
            <h2>{openedApp.name} <span style={{color:'var(--ct-muted, grey)'}}>{/* Fork addition: the skin's secondary text (grey was 4.4:1 on the dark card) */}{openedApp.appstore != 'cosmos-cloud' ? (' @ '+openedApp.appstore) : ''}</span></h2>
          </Stack>

          <div>
            {openedApp.tags && openedApp.tags.slice(0, 8).map((tag) => <Chip label={tag} />)}
          </div>

          <div>
            {openedApp.supported_architectures && openedApp.supported_architectures.slice(0, 8).map((tag) => <Chip label={tag} />)}
          </div>
          {/* Fork addition: said before the install, not at the pull (the review checks the image itself again) */}
          {!marketArchFits(openedApp.supported_architectures, API.CPU_ARCH) && <Alert severity="warning" data-market-arch>
            {t('navigation.market.archMismatch', 'This app is listed for {{list}} only, and this server is {{arch}}: it will most likely not start here.', { list: openedApp.supported_architectures.join(', '), arch: API.CPU_ARCH })}
          </Alert>}

          {openedApp.appstore != 'cosmos-cloud' && <div>
            <div>
            <Tooltip title={t('navigation.market.unofficialMarketTooltip')}>
                <WarningOutlined />
              </Tooltip> <strong>{t('global.source')}:</strong> {openedApp.appstore} 
            </div>
          </div>}
          
          <div style={{ overflowWrap: 'anywhere' }}>{/* Fork addition: a long address breaks instead of running out of a phone's screen */}
            <div><strong>{t('navigation.market.repository')}:</strong> <LinkMUI href={openedApp.repository}>{openedApp.repository}</LinkMUI></div>
            <div><strong>{t('navigation.market.image')}:</strong> <LinkMUI href={openedApp.image}>{openedApp.image}</LinkMUI></div>
            <div><strong>{t('navigation.market.compose')}:</strong> <LinkMUI href={openedApp.compose}>{openedApp.compose}</LinkMUI></div>
          </div>

          <div dangerouslySetInnerHTML={{ __html: DOMPurify.sanitize(openedApp?.translation?.[i18n?.resolvedLanguage]?.longDescription || openedApp?.translation?.[i18n?.resolvedLanguage.substr?.(0,2)]?.longDescription || openedApp.longDescription) }}></div>

          <DockerComposeImport installerInit defaultName={openedApp.name} dockerComposeInit={openedApp.compose} />
        </Stack>
      </Stack>
    </Box>}

    <Stack style={{ position: 'relative' }} spacing={1}>
      {/* Fork addition: on a phone the showcase is as tall as its content (band and text); nothing is kept for an empty one */}
      {!(loaded && (!showcase || !showcase.length)) && <Stack sx={{ height: { xs: 'auto', sm: '35vh' }, minHeight: { xs: loaded ? 0 : '35vh', sm: 0 } }} spacing={1}>
        {!loaded && <Box style={{
          width: '100%',
          height: '100%',
          zIndex: 1000,
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
        }}>
          <CircularProgress
            size={100}
          />
        </Box>}
        {showcase && showcase.length > 0 && <Showcases showcase={showcase} isDark={isDark} isAdmin={isAdmin} />}
      </Stack>}

      {/* Fork addition: the margins as sx, matching the page's padding at each width (16 px at the sides of a phone: -24 px ran 8 px out of the window) */}
      <Stack spacing={1} sx={{ mx: { xs: -2, sm: -3 }, mb: -3, p: { xs: 2, sm: 3 } }} style={{
        ...backgroundStyle,
        minHeight: 'calc(65vh - 80px)',
      }}>
        <h2>{t('navigation.market.applicationsTitle')}</h2>
        {/* Fork addition: useFlexGap flexWrap (on a phone the row ran out of the window) */}
        <Stack direction="row" spacing={2} useFlexGap sx={{ flexWrap: 'wrap' }}>
          <Input placeholder={t('navigation.market.search',  {count: filteredAppList.length})}
            value={search}
            style={{ maxWidth: '400px' }}
            startAdornment={
              <InputAdornment position="start">
                <SearchOutlined />
              </InputAdornment>
            }
            onChange={(e) => {
              setSearch(e.target.value);
            }}
          />

          <PermissionGuard permission={PERM_RESOURCES}>
            {/* Fork addition: a link styled as a button (one Tab stop) */}
            <ResponsiveButton
              component={Link}
              to="/cosmos-ui/servapps/new-service"
              variant="contained"
              startIcon={<AppstoreAddOutlined />}
            >{t('navigation.market.startServAppButton')}</ResponsiveButton>
          </PermissionGuard>
          <DockerComposeImport refresh={() => { }} />
          <EditSourcesModal onSave={refresh} />
          <PersistentCheckbox name="filterDups" label={t('navigation.market.filterDuplicateCheckbox')} value={filterDups} onChange={setFilterDups} />
        </Stack>
        {!loaded && (!apps || !Object.keys(apps).length) && <Box style={{
          width: '100%',
          height: '100%',
          zIndex: 1000,
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          marginTop: '150px',
        }}>
          <CircularProgress
            size={100}
          />
        </Box>}

        {/* Fork addition: said when no source answered (a server without a way out, a source that is down) */}
        {loaded && !filteredAppList.length && !search && <Alert severity="info" sx={{ maxWidth: 720 }}>
          {t('navigation.market.nothingLoaded', 'No app could be loaded from the market sources. Check that this server can reach the internet, or the addresses under "Sources".')}
        </Alert>}
        {loaded && !filteredAppList.length && search.length > 2 && <Typography variant="body2" sx={{ color: 'text.secondary' }}>
          {t('navigation.market.noMatch', 'No app matches “{{search}}”.', { search: search.slice(0, 80), interpolation: { escapeValue: false } })}
        </Typography>}
        {apps && Object.keys(apps).length > 0 && <Grid2 container spacing={{ xs: 1, sm: 1, md: 2 }}>
          {filteredAppList.map((app) => {
              return <Grid2
              style={{
                ...gridAnim,
                cursor: 'pointer',
              }} size={{ xs: 12, sm: 12, md: 6, lg: 4, xl: 3 }} key={app.name + app.appstore}><Link to={"/cosmos-ui/market-listing/" + app.appstore + "/" + app.name} style={{
                textDecoration: 'none',
              }}>
                  <div key={app.name} style={appCardStyle(theme)}>
                    <Stack spacing={3} direction={'row'} sx={{ alignItems: 'center' }} style={{ padding: '0px 15px' }}>
                      <img src={app.icon} style={{ width: 64, height: 64 }} />
                      <Stack spacing={1}>
                        <div style={{ fontWeight: "bold" }}>{app.name}<span style={{color:'var(--ct-muted, grey)'}}>{/* Fork addition: the skin's secondary text (grey was 4.4:1 on the dark card) */}{app.appstore != 'cosmos-cloud' ? (' @ '+app.appstore) : ''}</span></div>
                        <div style={{
                          height: '40px',
                          overflow: 'hidden',
                          textOverflow: 'ellipsis',
                          whiteSpace: 'pre-wrap',
                        }}
                        >{ app?.translation?.[i18n?.resolvedLanguage]?.description || app?.translation?.[i18n?.resolvedLanguage.substr?.(0,2)]?.description || app.description }</div>
                        <Stack direction={'row'} spacing={1}>
                          <div style={{
                            fontStyle: "italic", opacity: 1, /* Fork addition: was 0.7, which is 3:1 on white */
                            overflow: 'hidden',
                            height: '21px',
                            textOverflow: 'ellipsis',
                            whiteSpace: 'pre-wrap',
                          }}>{app.tags.slice(0, 3).join(", ")}</div>
                        </Stack>
                      </Stack>
                    </Stack>
                  </div>

                </Link>
              </Grid2>
            })}
        </Grid2>}
      </Stack>
    </Stack>
  </>
};

export default MarketPage;