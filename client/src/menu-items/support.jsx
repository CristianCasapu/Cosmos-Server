// assets
import { GithubOutlined, QuestionOutlined, BugOutlined, ForkOutlined, ShoppingCartOutlined } from '@ant-design/icons';
import { PURCHASE_URL } from '../utils/paidVisibility'; // Fork addition
import DiscordOutlined from '../assets/images/icons/discord.svg'
import DiscordOutlinedWhite from '../assets/images/icons/discord_white.svg'
import { useTheme } from '@mui/material/styles';

// ==============================|| MENU ITEMS - SAMPLE PAGE & DOCUMENTATION ||============================== //

const DiscordOutlinedIcon = (props) => {
    const theme = useTheme();
    return (
        <img src={
            theme.palette.mode === 'dark' ? DiscordOutlinedWhite : DiscordOutlined} width="16px" alt="Discord" {...props} />
    );
};

const support = {
    id: 'support',
    title: 'menu-items.support',
    type: 'group',
    children: [
        {
            id: 'discord',
            title: 'menu-items.support.discord',
            type: 'item',
            url: 'https://discord.com/invite/PwMWwsrwHA',
            icon: DiscordOutlinedIcon,
            external: true,
            target: true
        },
        {
            id: 'github',
            title: 'menu-items.support.github',
            type: 'item',
            url: 'https://github.com/azukaar/Cosmos-Server',
            icon: GithubOutlined,
            external: true,
            target: true
        },
        // Fork addition: this build comes from a fork; link to its source.
        {
            id: 'fork',
            title: 'menu-items.support.fork',
            type: 'item',
            url: 'https://github.com/CristianCasapu/Cosmos-Server',
            icon: ForkOutlined,
            external: true,
            target: true
        },
        {
            id: 'documentation',
            title: 'menu-items.support.docsTitle',
            type: 'item',
            url: 'https://cosmos-cloud.io/doc',
            icon: QuestionOutlined,
            external: true,
            target: true
        },
        {
            id: 'bug',
            title: 'menu-items.support.bugReportTitle',
            type: 'item',
            url: 'https://github.com/azukaar/Cosmos-Server/issues/new/choose',
            icon: BugOutlined,
            external: true,
            target: true
        },
        // Fork addition: the one pointer to buying a licence, shown only without
        // one (components/paidVisibility.jsx); opens the original project's
        // checkout in a new tab (noopener noreferrer, see NavItem).
        {
            id: 'purchase-premium',
            title: 'menu-items.support.purchasePremium',
            type: 'item',
            url: PURCHASE_URL,
            icon: ShoppingCartOutlined,
            external: true,
            target: true,
            forkPurchase: true
        }
    ]
};

export default support;
