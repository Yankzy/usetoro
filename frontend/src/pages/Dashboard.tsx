
import { useGetWebhooksQuery, useReplayWebhookMutation } from '../store/dashboard/dashboardApi';
import { Icons } from '../components/Icons';
import { useNavigate } from 'react-router-dom';
import { logout } from '../store/auth/authSlice';
import { useDispatch } from 'react-redux';

const Dashboard = () => {
    const { data: webhooks, isLoading, refetch } = useGetWebhooksQuery();
    const [replayWebhook, { isLoading: isReplaying }] = useReplayWebhookMutation();
    const dispatch = useDispatch();
    const navigate = useNavigate();

    const handleLogout = () => {
        dispatch(logout());
        navigate('/');
    };

    const handleReplay = async (id: string) => {
        try {
            await replayWebhook(id).unwrap();
            refetch(); // Refresh list to see any status updates
        } catch (error) {
            console.error('Replay failed', error);
        }
    };

    return (
        <div className="min-h-screen bg-toro-bg text-white font-sans">
            {/* Simple Navbar for Dashboard */}
            <nav className="border-b border-toro-border bg-[#050505] px-6 py-4 flex justify-between items-center">
                <div className="flex items-center gap-2 font-mono text-xl font-bold tracking-tighter">
                    <Icons.Logo className="w-8 h-8 text-green-500" />
                    <span className="pt-1">TORO DASHBOARD</span>
                </div>
                <button
                    onClick={handleLogout}
                    className="text-sm font-medium text-zinc-400 hover:text-white transition-colors"
                >
                    Sign Out
                </button>
            </nav>

            <main className="max-w-7xl mx-auto px-6 py-8">
                <div className="flex justify-between items-center mb-8">
                    <h1 className="text-2xl font-bold">Recent Webhooks</h1>
                    <button onClick={() => refetch()} className="p-2 rounded bg-zinc-900 hover:bg-zinc-800 text-zinc-400 hover:text-white transition-colors">
                        <Icons.RotateCw className="w-4 h-4" />
                    </button>
                </div>

                {isLoading ? (
                    <div className="text-center py-12 text-zinc-500">Loading webhooks...</div>
                ) : (
                    <div className="bg-[#0A0A0A] border border-toro-border rounded-xl shadow-sm overflow-hidden font-mono text-sm">
                        <div className="grid grid-cols-[120px_1fr_100px_100px] gap-4 px-4 py-3 bg-zinc-900/50 border-b border-toro-border text-xs font-bold text-zinc-500 uppercase tracking-wider">
                            <div>Date</div>
                            <div>Source / ID</div>
                            <div>Status</div>
                            <div className="text-right">Action</div>
                        </div>
                        <div className="divide-y divide-zinc-800">
                            {webhooks?.map((webhook) => (
                                <div key={webhook.id} className="grid grid-cols-[120px_1fr_100px_100px] gap-4 px-4 py-3 items-center hover:bg-zinc-900/30 transition-colors">
                                    <div className="text-zinc-500 text-xs">
                                        {new Date(webhook.created_at).toLocaleTimeString()}
                                    </div>
                                    <div className="overflow-hidden">
                                        <div className="font-bold text-zinc-300">{webhook.source}</div>
                                        <div className="text-xs text-zinc-600 truncate">{webhook.id}</div>
                                    </div>
                                    <div>
                                        <span className={`inline-flex items-center px-2 py-0.5 rounded text-xs font-medium 
                                            ${webhook.status === 'received' ? 'bg-blue-500/10 text-blue-500' :
                                                webhook.status === 'processed' ? 'bg-green-500/10 text-green-500' : 'bg-red-500/10 text-red-500'}`}>
                                            {webhook.status}
                                        </span>
                                    </div>
                                    <div className="text-right">
                                        <button
                                            onClick={() => handleReplay(webhook.id)}
                                            className="text-toro-green hover:text-green-400 transition-colors text-xs font-bold flex items-center justify-end gap-1 ml-auto"
                                            disabled={isReplaying}
                                        >
                                            <Icons.Play className="w-3 h-3" /> Replay
                                        </button>
                                    </div>
                                </div>
                            ))}
                            {(!webhooks || webhooks.length === 0) && (
                                <div className="p-8 text-center text-zinc-500">
                                    No webhooks received yet.
                                </div>
                            )}
                        </div>
                    </div>
                )}
            </main>
        </div>
    );
};

export default Dashboard;
