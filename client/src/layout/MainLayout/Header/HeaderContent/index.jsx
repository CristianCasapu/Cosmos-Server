import React from 'react';

// material-ui
import { Box, Chip, IconButton, Link, Stack, useMediaQuery } from '@mui/material';
import { GithubOutlined } from '@ant-design/icons';

// project import
import Search from './Search';
import Profile from './Profile';
import Notification from './Notification';
import MobileSection from './MobileSection';
import Jobs from './jobs';
import { useTranslation } from 'react-i18next';
import RestartMenu from './restartMenu';
import TerminalHeader from './terminal';
import SudoModal from './sudoModal';
import ModeToggle from '../../../../components/modeToggle'; // Fork addition: the skin's light/dark switch
import { PWAInstallButton } from '../../../../components/pwa'; // Fork addition: install the panel as an app
import HeaderMore from '../../../../components/headerMore'; // Fork addition: on phones the terminal and the restarts are in "More"

// ==============================|| HEADER - CONTENT ||============================== //

const HeaderContent = () => {
    const { t } = useTranslation();
    const matchesXs = useMediaQuery((theme) => theme.breakpoints.down('md'));
    const [terminal, setTerminal] = React.useState(false); // Fork addition: opened from "More" on a phone
    const onPhone = { display: { xs: 'none', sm: 'inline-flex' } }; // Fork addition: in "More" below 600 px

    return (
        <>
            {!matchesXs && <Search />}
            {matchesXs && <Box sx={{ width: '100%', ml: 1 }} />}

            <Stack direction="row" spacing={2}>
                <TerminalHeader sx={onPhone} open={terminal} onClose={() => setTerminal(false)} />
                <Jobs />
                <Notification />
                <ModeToggle sx={{ display: { xs: 'none', sm: 'inline-flex' } }} />{/* Fork addition: on phones it is in the menu's footer */}
                <PWAInstallButton sx={{ display: { xs: 'none', sm: 'inline-flex' } }} />{/* Fork addition: only while the browser can install it; on phones in the menu's footer */}

                <SudoModal />
                {/* Fork addition: the chip is the link (one Tab stop; it was a chip inside a link), a finger's height on a phone */}
                <Chip component="a" href="/cosmos-ui/logout" clickable label={t('global.logout')} sx={{ height: { xs: 44, sm: 36 } }} />
                <Box sx={onPhone}><RestartMenu /></Box>
                <HeaderMore sx={{ display: { xs: 'inline-flex', sm: 'none' } }} onTerminal={() => setTerminal(true)} />
            </Stack>
            {/* {!matchesXs && <Profile />}
            {matchesXs && <MobileSection />} */}
        </>
    );
};

export default HeaderContent;
