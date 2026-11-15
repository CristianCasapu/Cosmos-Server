import { useEffect, useRef, useState } from 'react';
import { Link as RouterLink } from 'react-router'; // Fork addition: every item opens its details
import { useTranslation } from 'react-i18next';
import dayjs from 'dayjs';

// material-ui
import { useTheme } from '@mui/material/styles';
import {
    Avatar,
    Badge,
    Box,
    ClickAwayListener,
    Divider,
    IconButton,
    Tooltip, // Fork addition: Tooltip, for the icon-only buttons
    List,
    ListItemButton,
    ListItemAvatar,
    ListItemText,
    Button, // Fork addition: "See all"
    Paper,
    Popper,
    Typography,
    useMediaQuery
} from '@mui/material';

// project import
import MainCard from '../../../../components/MainCard';
import Transitions from '../../../../components/@extended/Transitions';
// assets
import { BellOutlined, CheckCircleOutlined, CheckOutlined, CloseOutlined, ExclamationCircleOutlined, InfoCircleOutlined, WarningOutlined } from '@ant-design/icons';

import * as API from '../../../../api';
// Fork addition: the bell is a preview of the notifications page (pages/notifications)
import { BELL_SIZE, TEXT, cleanItems, detailsLink, markAllBody, messageOf, titleOf } from '../../../../utils/notifications';

// sx styles
const avatarSX = {
    width: 36,
    height: 36,
    fontSize: '1rem'
};


// ==============================|| HEADER CONTENT - NOTIFICATION ||============================== //

// Fork addition: the bell shows the newest notifications as a preview; each
// one opens its details, "See all" the page. Opening the bell no longer
// marks everything read (a notification is read once its details are
// opened, or with "Mark all as read").
const Notification = () => {
    const { t } = useTranslation();
    const theme = useTheme();
    const matchesXs = useMediaQuery(theme.breakpoints.down('md'));
    const [notifications, setNotifications] = useState([]);
    const [unread, setUnread] = useState(0);

    const refreshNotifications = () => {
        API.notifications.list({ limit: BELL_SIZE }, true).then((res) => {
            const data = (res && res.data) || {};
            setNotifications(cleanItems(data.items));
            setUnread(Number(data.unread) || 0);
        }).catch(() => {}); // a poll that fails (the server restarts after a save) is tried again by the next one
    };

    const markAllRead = () => {
        API.notifications.mark(markAllBody()).then(() => {
            setNotifications((list) => list.map((n) => ({ ...n, read: true })));
            setUnread(0);
        }).catch(() => {});
    };

    useEffect(() => {
        refreshNotifications();

        const interval = setInterval(() => {
            refreshNotifications();
        }, 20000);

        return () => clearInterval(interval);
    }, []);

    const anchorRef = useRef(null);
    const [open, setOpen] = useState(false);

    const handleToggle = () => {
        if (!open) {
            refreshNotifications();
        }
        setOpen((prevOpen) => !prevOpen);
    };

    const handleClose = (event) => {
        if (anchorRef.current && anchorRef.current.contains(event.target)) {
            return;
        }
        setOpen(false);
    };

    const ICON = { warning: WarningOutlined, error: ExclamationCircleOutlined, success: CheckCircleOutlined, info: InfoCircleOutlined };
    const getNotifIcon = (notification) => {
        const Icon = ICON[notification.level] || InfoCircleOutlined;
        return <Avatar sx={{ color: notification.level + '.main', bgcolor: notification.level + '.lighter' }}><Icon /></Avatar>;
    };

    const iconBackColor = theme.palette.mode === 'dark' ? 'grey.700' : 'grey.100';
    const iconBackColorOpen = theme.palette.mode === 'dark' ? 'grey.800' : 'grey.200';

    return (
        <Box sx={{ flexShrink: 0, ml: 0.75 }}>
            <Tooltip title={open ? '' : t('header.notificationTitle', 'Notification')}><IconButton
                disableRipple
                color="secondary"
                sx={{ color: 'text.primary', bgcolor: open ? iconBackColorOpen : iconBackColor }}
                aria-label={t('header.notificationTitle', 'Notification')}
                ref={anchorRef}
                aria-controls={open ? 'profile-grow' : undefined}
                aria-haspopup="true"
                onClick={handleToggle}
            >
                <Badge badgeContent={unread} color="error">
                    <BellOutlined />
                </Badge>
            </IconButton></Tooltip>
            <Popper
                placement={matchesXs ? 'bottom' : 'bottom-end'}
                open={open}
                anchorEl={anchorRef.current}
                role={undefined}
                transition
                disablePortal
                popperOptions={{
                    modifiers: [
                        {
                            name: 'offset',
                            options: {
                                offset: [matchesXs ? -5 : 0, 9]
                            }
                        },
                        // the page's 16 px margin on both sides (it touched the right edge on a phone)
                        { name: 'preventOverflow', options: { padding: 16 } }
                    ]
                }}
            >
                {({ TransitionProps }) => (
                    <Transitions type="fade" in={open} {...TransitionProps}>
                        <Paper
                            sx={{
                                boxShadow: theme.customShadows.z1,
                                width: '100%',
                                minWidth: 285,
                                maxWidth: 420,
                                [theme.breakpoints.down('md')]: {
                                    maxWidth: 285
                                },
                                // on a phone the whole width but the page's margins
                                [theme.breakpoints.down('sm')]: {
                                    width: 'calc(100vw - 32px)',
                                    maxWidth: 'calc(100vw - 32px)',
                                }
                            }}
                        >
                            <ClickAwayListener onClickAway={handleClose}>
                                <MainCard
                                    title={t(...TEXT.bellTitle)}
                                    elevation={0}
                                    border={false}
                                    content={false}
                                    secondary={<Box sx={{ display: 'flex', gap: 0.5 }}>
                                        {unread > 0 && <Tooltip title={t(...TEXT.markAllRead)}><IconButton size="small" aria-label={t(...TEXT.markAllRead)} onClick={markAllRead} sx={{ minWidth: { xs: 44, sm: 30 }, minHeight: { xs: 44, sm: 30 } }}>
                                            <CheckOutlined />
                                        </IconButton></Tooltip>}
                                        <Tooltip title={t('global.close', 'Close')}><IconButton size="small" aria-label={t('global.close', 'Close')} onClick={handleToggle} sx={{ minWidth: { xs: 44, sm: 30 }, minHeight: { xs: 44, sm: 30 } }}>
                                            <CloseOutlined />
                                        </IconButton></Tooltip>
                                    </Box>}
                                >
                                    <List
                                        component="nav"
                                        sx={{
                                            maxHeight: 'min(420px, calc(100vh - 220px))',
                                            overflow: 'auto',
                                            p: 0,
                                            '& .MuiListItemButton-root': {
                                                py: 0.75,
                                                minHeight: 44,
                                                '& .MuiAvatar-root': avatarSX,
                                            }
                                        }}
                                    >
                                        {notifications.map(notification => (<div key={notification.id}>
                                            <ListItemButton component={RouterLink} to={detailsLink(notification)} onClick={() => setOpen(false)}
                                            data-notification={notification.id}
                                            style={{
                                                borderLeft: notification.read ? 'none' : `4px solid ${theme.palette[notification.level] ? theme.palette[notification.level].main : theme.palette.info.main}`,
                                                paddingLeft: notification.read ? '14px' : '10px',
                                            }}>
                                                <ListItemAvatar>
                                                    {getNotifIcon(notification)}
                                                </ListItemAvatar>
                                                <ListItemText
                                                    primary={<>
                                                        <Typography variant={notification.read ? 'body1' : 'h6'} component="div" sx={{ overflowWrap: 'anywhere' }}>
                                                            {titleOf(notification, t)}{notification.count > 1 ? ' ×' + notification.count : ''}
                                                        </Typography>
                                                        <div style={{
                                                            overflow: 'hidden',
                                                            display: '-webkit-box',
                                                            WebkitLineClamp: 3,
                                                            WebkitBoxOrient: 'vertical',
                                                            overflowWrap: 'anywhere',
                                                            borderLeft: '1px solid grey',
                                                            paddingLeft: '8px',
                                                            margin: '2px'
                                                        }}>
                                                            {messageOf(notification, t)}
                                                        </div>
                                                        <Typography variant="caption" color="textSecondary" component="div">
                                                            {dayjs(notification.date).fromNow()}
                                                        </Typography></>
                                                    }
                                                />
                                            </ListItemButton>
                                        <Divider /></div>))}
                                        {notifications.length === 0 && <Typography variant="body1" color="textSecondary" sx={{ px: 2.5, py: 2 }}>
                                            {t(...TEXT.bellEmpty)}
                                        </Typography>}
                                    </List>
                                    <Box sx={{ p: 1 }}>
                                        <Button fullWidth component={RouterLink} to="/cosmos-ui/notifications" onClick={() => setOpen(false)} style={{ minHeight: 44 }}>
                                            {t(...TEXT.seeAll)}
                                        </Button>
                                    </Box>
                                </MainCard>
                            </ClickAwayListener>
                        </Paper>
                    </Transitions>
                )}
            </Popper>
        </Box>
    );
};

export default Notification;
