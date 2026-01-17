import React, { useState, useEffect } from 'react';
import { Link } from 'react-router-dom';
import { Icons } from '../components/Icons';
import { RevealOnScroll } from '../components/RevealOnScroll';
import { SalesContactModal } from '../components/SalesContactModal';
import { ArchitectureRow } from '../components/ArchitectureRow';
import { DashboardDemo } from '../components/DashboardDemo';

const SectionTitle = ({ children, badge }: { children: React.ReactNode; badge: string }) => (
    <RevealOnScroll className="text-center max-w-2xl mx-auto mb-16">
        <div className="inline-flex items-center gap-2 px-3 py-1 rounded-full bg-zinc-900 border border-toro-border text-xs text-green-400 font-mono mb-6 hover:scale-105 transition-transform cursor-default">
            {badge}
        </div>
        <h2 className="text-3xl md:text-5xl font-bold tracking-tight mb-4">{children}</h2>
    </RevealOnScroll>
);

const WaitlistForm = ({ id: _id }: { id?: string }) => {
    // id prop is available if needed for specific form identification
    const [email, setEmail] = useState('');
    const [submitted, setSubmitted] = useState(false);
    const [loading, setLoading] = useState(false);
    const WEBHOOK_URL = "https://script.google.com/macros/s/AKfycbyJVlAXnOsvYkarrZ9oWV5yEHpn0SgmSap2D5WvWyYEG6UkbrNG1DptpLqHDtlhKL1_PA/exec";
    const TAB_NAME = "Sheet1";

    const handleSubmit = async (e: React.FormEvent) => {
        e.preventDefault();
        if (!email) return;
        setLoading(true);
        try {
            if (WEBHOOK_URL) {
                await fetch(WEBHOOK_URL, {
                    method: 'POST',
                    mode: 'no-cors',
                    headers: { 'Content-Type': 'text/plain' },
                    body: JSON.stringify({ email, sheetName: TAB_NAME })
                });
            } else {
                await new Promise(resolve => setTimeout(resolve, 800));
            }
            setSubmitted(true);
            setEmail('');
        } catch (error) {
            console.error("Error:", error);
            setSubmitted(true);
            setEmail('');
        } finally {
            setLoading(false);
        }
    };

    return (
        <form onSubmit={handleSubmit} className="flex flex-col sm:flex-row gap-3 max-w-md w-full relative z-20" name="waitlist">
            <input
                type="email"
                name="email"
                placeholder="developer@startup.com"
                className="bg-zinc-900/50 border border-toro-border rounded-lg px-4 py-3 flex-1 focus:outline-none focus:border-green-500 transition-colors placeholder:text-zinc-600 text-white"
                value={email}
                onChange={(e) => setEmail(e.target.value)}
                required
                disabled={submitted || loading}
            />
            <button
                type="submit"
                className={`font-semibold px-6 py-3 rounded-lg transition-all flex items-center justify-center gap-2 min-w-[140px] 
                    ${submitted ? 'bg-green-600 text-white' : 'bg-white text-black hover:bg-zinc-200 hover:scale-105'}
                    ${loading ? 'opacity-80 cursor-wait' : ''}
                `}
                disabled={submitted || loading}
            >
                {loading ? "Joining..." : (submitted ? <><Icons.Check className="w-4 h-4" /> Joined</> : "Join Waitlist")}
            </button>
        </form>
    );
};

const LandingPage = () => {
    const [logs] = useState([
        { id: 1, text: "➜ ~ toro listen --port 3000", color: "text-zinc-300", delay: 0 },
        { id: 2, text: "Authenticated as @cachecowboy", color: "text-zinc-500", delay: 800 },
        { id: 3, text: "✔ Tunnel Established: https://api.usetoro.io/h/8291", color: "text-green-500", delay: 1600 },
        { id: 4, isDivider: true, delay: 1600 },
    ]);
    const [dynamicLogs, setDynamicLogs] = useState<any[]>([]);
    const [isMenuOpen, setIsMenuOpen] = useState(false);
    const [isContactOpen, setIsContactOpen] = useState(false);
    const [selectedTier, setSelectedTier] = useState({ name: '', price: '' });

    const openContact = (name: string, price: string) => {
        setSelectedTier({ name, price });
        setIsContactOpen(true);
    };

    const toggleMenu = () => setIsMenuOpen(!isMenuOpen);

    useEffect(() => {
        document.title = "Toro | The AI-Native Stability Layer";
        const faviconUrl = "data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' viewBox='0 0 24 24' fill='none' stroke='%2322c55e' stroke-width='2' stroke-linecap='round' stroke-linejoin='round'%3E%3Cpath d='M2 8a2 2 0 0 1 2-2h16a2 2 0 0 1 2 2v3c0 6-7 11-10 11S2 17 2 11V8z' /%3E%3Cpath d='M2 6L5 2' /%3E%3Cpath d='M22 6L19 2' /%3E%3Cline x1='12' y1='11' x2='12' y2='17' /%3E%3Cline x1='9' y1='14' x2='15' y2='14' /%3E%3C/svg%3E";
        let link = document.querySelector("link[rel~='icon']") as HTMLLinkElement;
        if (!link) {
            link = document.createElement('link');
            link.rel = 'icon';
            document.head.appendChild(link);
        }
        link.href = faviconUrl;
    }, []);

    useEffect(() => {
        const traffic = [
            { time: "[20:01:42]", method: "POST", path: "/stripe/webhook", status: "200 OK", statusColor: "text-green-500", latency: "(12ms)" },
            { time: "[20:01:45]", method: "POST", path: "/supabase/write", status: "QUEUED", statusColor: "text-yellow-500", latency: "(Buffered)" },
            { time: "[20:01:46]", method: "POST", path: "/plaid/sync", status: "PROCESSING", statusColor: "text-blue-400", latency: "..." },
        ];
        let timeouts: any[] = [];
        traffic.forEach((log, index) => {
            const timeout = setTimeout(() => {
                setDynamicLogs(prev => [...prev, log]);
            }, 2500 + (index * 1200));
            timeouts.push(timeout);
        });
        return () => timeouts.forEach(clearTimeout);
    }, []);

    return (
        <div className="min-h-screen text-white font-sans selection:bg-green-500/30 overflow-x-hidden">
            {/* Navbar */}
            <nav className="fixed top-0 w-full z-50 bg-[#050505]/80 backdrop-blur-md border-b border-toro-border">
                <div className="flex items-center justify-between px-6 py-4 max-w-7xl mx-auto">
                    <div className="flex items-center gap-2 font-mono text-xl font-bold tracking-tighter cursor-pointer hover:opacity-80 transition-opacity" onClick={() => window.scrollTo(0, 0)}>
                        <Icons.Logo className="w-8 h-8 text-green-500" />
                        <span className="pt-1">TORO</span>
                    </div>

                    <div className="hidden md:flex gap-8 text-sm font-medium text-zinc-400">
                        <a href="#problem" className="hover:text-white transition-colors">The Trap</a>
                        <a href="#features" className="hover:text-white transition-colors">Features</a>
                        <a href="#pricing" className="hover:text-white transition-colors">Pricing</a>
                        <a href="#faq" className="hover:text-white transition-colors">FAQ</a>
                    </div>

                    <div className="hidden md:block">
                        <Link to="/login" className="text-sm bg-zinc-900 hover:bg-zinc-800 text-white px-4 py-2 rounded-lg border border-toro-border transition-colors">
                            Login
                        </Link>
                    </div>

                    <button className="md:hidden flex flex-col justify-center gap-1.5 w-8 h-8 z-50" onClick={toggleMenu}>
                        <span className={`block w-full h-0.5 bg-white transition-all duration-300 ${isMenuOpen ? 'rotate-45 translate-y-2' : ''}`}></span>
                        <span className={`block w-full h-0.5 bg-white transition-all duration-300 ${isMenuOpen ? 'opacity-0' : ''}`}></span>
                        <span className={`block w-full h-0.5 bg-white transition-all duration-300 ${isMenuOpen ? '-rotate-45 -translate-y-2' : ''}`}></span>
                    </button>
                </div>

                <div className={`md:hidden absolute top-full left-0 w-full bg-[#050505] border-b border-toro-border shadow-2xl transition-all duration-300 overflow-hidden ${isMenuOpen ? 'max-h-screen opacity-100 py-6' : 'max-h-0 opacity-0 py-0'}`}>
                    <div className="flex flex-col items-center gap-6 text-lg font-medium text-zinc-300">
                        <a href="#problem" onClick={() => setIsMenuOpen(false)} className="hover:text-white transition-colors">The Trap</a>
                        <a href="#features" onClick={() => setIsMenuOpen(false)} className="hover:text-white transition-colors">Features</a>
                        <a href="#pricing" onClick={() => setIsMenuOpen(false)} className="hover:text-white transition-colors">Pricing</a>
                        <a href="#faq" onClick={() => setIsMenuOpen(false)} className="hover:text-white transition-colors">FAQ</a>
                        <div className="h-px bg-zinc-800 w-1/3"></div>
                        <Link to="/login" onClick={() => setIsMenuOpen(false)} className="text-sm bg-zinc-900 hover:bg-zinc-800 text-white px-8 py-3 rounded-lg border border-toro-border transition-colors">
                            Login
                        </Link>
                    </div>
                </div>
            </nav>

            {/* Hero Section */}
            <main className="max-w-7xl mx-auto px-6 pt-32 pb-24 relative">
                <div className="absolute inset-0 grid-bg opacity-[0.15] -z-10 pointer-events-none mask-gradient animate-grid-flow"></div>

                <div className="grid lg:grid-cols-2 gap-16 items-center w-full max-w-full">
                    <RevealOnScroll className="space-y-8 z-10 min-w-0">
                        <h1 className="text-4xl md:text-5xl lg:text-7xl font-bold tracking-tight leading-[1.1]">
                            The <span className="text-transparent bg-clip-text bg-gradient-to-r from-purple-400 to-pink-600">AI-Native</span> Stability Layer for API Integrations.
                        </h1>
                        <p className="text-lg md:text-xl text-zinc-400 max-w-md leading-relaxed">
                            Stop crashing your database with AI agents. Stop missing webhooks.
                            Toro provides the queues, tunnels, and agents so you can just build.
                        </p>
                        <WaitlistForm id="hero-form" />
                        <div className="flex items-center gap-4 text-xs text-zinc-500 font-mono pt-4">
                            <span>TRUSTED BY BUILDERS AT</span>
                            <div className="h-px bg-zinc-800 flex-1"></div>
                        </div>
                        <div className="flex gap-6 opacity-50 grayscale hover:grayscale-0 transition-all cursor-default overflow-x-auto pb-2 no-scrollbar">
                            <span className="font-bold text-lg hover:text-white transition-colors whitespace-nowrap">N8N</span>
                            <span className="font-bold text-lg hover:text-white transition-colors whitespace-nowrap">Supabase</span>
                            <span className="font-bold text-lg hover:text-white transition-colors whitespace-nowrap">Stripe</span>
                            <span className="font-bold text-lg hover:text-white transition-colors whitespace-nowrap">Plaid</span>
                        </div>
                    </RevealOnScroll>

                    {/* Terminal Visual */}
                    <RevealOnScroll delay={200} className="relative w-full max-w-[calc(100vw-3rem)] md:max-w-full">
                        <div className="absolute -inset-1 bg-gradient-to-r from-green-500 to-emerald-600 rounded-xl blur opacity-20 animate-pulse-slow"></div>
                        <div className="relative bg-[#0A0A0A] border border-toro-border rounded-xl shadow-2xl overflow-hidden font-mono text-sm h-[360px] flex flex-col hover:border-green-500/30 transition-colors duration-500">
                            <div className="flex items-center gap-2 px-4 py-3 bg-zinc-900/50 border-b border-toro-border">
                                <div className="flex gap-1.5">
                                    <div className="w-3 h-3 rounded-full bg-red-500/20 border border-red-500/30"></div>
                                    <div className="w-3 h-3 rounded-full bg-yellow-500/20 border border-yellow-500/30"></div>
                                    <div className="w-3 h-3 rounded-full bg-green-500/20 border border-green-500/30"></div>
                                </div>
                                <div className="ml-2 text-zinc-600 text-xs">toro — -zsh — 80x24</div>
                            </div>
                            <div className="p-6 space-y-2 text-zinc-300 overflow-y-auto overflow-x-auto terminal-scroll flex-1">
                                <div className="min-w-[400px]">
                                    {logs.map((log: any) => (
                                        <div key={log.id} className={`transition-opacity duration-500 ${log.isDivider ? 'border-t border-toro-border my-4 opacity-50' : ''}`} style={{ animation: `fadeIn 0.5s ease-out ${log.delay}ms forwards`, opacity: 0 }}>
                                            {!log.isDivider && <span className={log.color}>{log.text}</span>}
                                        </div>
                                    ))}
                                    {dynamicLogs.map((log, i) => (
                                        <div key={i} className="flex gap-3 animate-[slideUp_0.3s_ease-out]">
                                            <span className="text-zinc-600">{log.time}</span>
                                            <span className="text-purple-400">{log.method}</span>
                                            <span>{log.path}</span>
                                            <span className={log.statusColor}>{log.status}</span>
                                            <span className="text-zinc-600 ml-auto">{log.latency}</span>
                                        </div>
                                    ))}
                                    <div className="w-2 h-4 bg-zinc-500 mt-2 cursor-blink"></div>
                                </div>
                            </div>
                        </div>
                    </RevealOnScroll>
                </div>
            </main>

            {/* Happy Developer Section */}
            <section className="py-24 border-b border-toro-border bg-[#050505] overflow-hidden">
                <div className="max-w-7xl mx-auto px-6">
                    <RevealOnScroll>
                        <div className="grid md:grid-cols-2 gap-12 items-center">
                            <div className="relative group">
                                <div className="hidden md:block absolute -inset-1 bg-gradient-to-r from-green-500 to-teal-500 rounded-2xl blur opacity-25 group-hover:opacity-40 transition duration-1000"></div>
                                <div className="relative rounded-2xl overflow-hidden border border-zinc-800 shadow-2xl bg-zinc-900 md:aspect-[4/3]">
                                    <img
                                        src="/happy_developer.png"
                                        alt="Happy Developer"
                                        className="w-full h-auto md:h-full md:object-cover grayscale group-hover:grayscale-0 transition-all duration-700"
                                    />
                                </div>
                            </div>

                            <div className="space-y-12 min-w-0">
                                <h3 className="text-2xl md:text-3xl font-mono text-zinc-300 italic leading-relaxed">
                                    "I never want to think about broken payloads, retries, cursors, missed webhooks, or schema drift ever again."
                                </h3>
                                <div className="space-y-6">
                                    <div className="text-xs font-mono text-zinc-600 uppercase tracking-widest">Works with everything</div>
                                    <div className="relative w-full overflow-hidden mask-gradient-x">
                                        <div className="flex gap-12 animate-scroll-left w-max hover:pause">
                                            {[...Array(2)].map((_, i) => (
                                                <React.Fragment key={i}>
                                                    <span className="font-bold text-2xl text-zinc-600 hover:text-white transition-colors cursor-default">Stripe</span>
                                                    <span className="font-bold text-2xl text-zinc-600 hover:text-white transition-colors cursor-default">QuickBooks</span>
                                                    <span className="font-bold text-2xl text-zinc-600 hover:text-white transition-colors cursor-default">Plaid</span>
                                                    <span className="font-bold text-2xl text-zinc-600 hover:text-white transition-colors cursor-default">Slack</span>
                                                    <span className="font-bold text-2xl text-zinc-600 hover:text-white transition-colors cursor-default">Zendesk</span>
                                                    <span className="font-bold text-2xl text-zinc-600 hover:text-white transition-colors cursor-default">Shopify</span>
                                                </React.Fragment>
                                            ))}
                                        </div>
                                    </div>
                                </div>
                            </div>
                        </div>
                    </RevealOnScroll>
                </div>
            </section>

            {/* Problem Section */}
            <section id="problem" className="py-24 border-t border-toro-border bg-zinc-900/10">
                <div className="max-w-7xl mx-auto px-6">
                    <SectionTitle badge="INTEGRATION TRAP">Why your stack is crashing</SectionTitle>
                    <div className="grid md:grid-cols-3 gap-8 mt-12">
                        <RevealOnScroll delay={0} className="p-8 rounded-2xl bg-zinc-900/30 border border-toro-border relative overflow-hidden group hover:bg-zinc-900/50 transition-colors">
                            <div className="absolute top-0 right-0 p-4 opacity-20 group-hover:opacity-100 transition-opacity transform group-hover:scale-110 duration-300">
                                <Icons.Zap className="w-12 h-12 text-yellow-500" />
                            </div>
                            <h3 className="text-xl font-bold mb-4">Connection Exhaustion</h3>
                            <p className="text-zinc-400">
                                AI Agents in N8N are chaotic. When 50 agents try to write to Postgres at once, you hit the <span className="text-red-400 font-mono bg-red-900/20 px-1 rounded">max_connections</span> limit and everything dies.
                            </p>
                        </RevealOnScroll>
                        <RevealOnScroll delay={100} className="p-8 rounded-2xl bg-zinc-900/30 border border-toro-border relative overflow-hidden group hover:bg-zinc-900/50 transition-colors">
                            <div className="absolute top-0 right-0 p-4 opacity-20 group-hover:opacity-100 transition-opacity transform group-hover:scale-110 duration-300">
                                <Icons.Terminal className="w-12 h-12 text-blue-500" />
                            </div>
                            <h3 className="text-xl font-bold mb-4">Webhook Hell</h3>
                            <p className="text-zinc-400">
                                Debugging webhooks usually means network timeouts or blind guessing. If your server crashes, that webhook data is gone forever.
                            </p>
                        </RevealOnScroll>
                        <RevealOnScroll delay={200} className="p-8 rounded-2xl bg-zinc-900/30 border border-toro-border relative overflow-hidden group hover:bg-zinc-900/50 transition-colors">
                            <div className="absolute top-0 right-0 p-4 opacity-20 group-hover:opacity-100 transition-opacity transform group-hover:scale-110 duration-300">
                                <Icons.Activity className="w-12 h-12 text-purple-500" />
                            </div>
                            <h3 className="text-xl font-bold mb-4">Integration Fatigue</h3>
                            <p className="text-zinc-400">
                                Integrating Stripe, Plaid, QuickBooks and other APIs means writing perfect endpoints or infinite polling loops and managing cursors. It's tidious, boring, and you will maintain it for for the rest of your life.
                            </p>
                        </RevealOnScroll>
                    </div>
                </div>
            </section>

            {/* Feature Deep Dives */}
            <div id="features" className="space-y-0">
                {/* 1. The Airbag */}
                <section className="py-24 border-t border-toro-border relative overflow-hidden">
                    <div className="max-w-7xl mx-auto px-6 grid lg:grid-cols-2 gap-16 items-center">
                        <RevealOnScroll>
                            <div className="w-12 h-12 bg-zinc-800 rounded-lg flex items-center justify-center mb-6 text-yellow-500 animate-float">
                                <Icons.Shield className="w-6 h-6" />
                            </div>
                            <h2 className="text-4xl font-bold mb-6">AI-Powered Buffer. <br /><span className="text-zinc-500">Queue first, write later.</span></h2>
                            <p className="text-lg text-zinc-400 mb-8 leading-relaxed">
                                We handle the chaos so your database doesn't have to. Toro sits between your AI agents and your infrastructure, absorbing traffic spikes and messy inputs instantly, then writing clean, safe data to your database at a pace it can handle.
                            </p>
                            <ul className="space-y-4 text-zinc-300">
                                {[
                                    "Protects against traffic spikes",
                                    "AI Schema Repair (Fixes bad JSON automatically)",
                                    "Zero-config connection pooling"
                                ].map((text, i) => (
                                    <li key={i} className="flex items-center gap-3">
                                        <Icons.Check className="w-5 h-5 text-green-500" />
                                        <span>{text}</span>
                                    </li>
                                ))}
                            </ul>
                        </RevealOnScroll>
                        <RevealOnScroll delay={200} className="bg-[#0A0A0A] border border-toro-border rounded-xl p-8 shadow-2xl relative animate-float">
                            <div className="absolute top-4 right-4 text-xs font-mono text-zinc-600">LIVE MONITOR</div>
                            <div className="space-y-6">
                                <div>
                                    <div className="flex justify-between text-xs font-mono text-zinc-500 mb-2">
                                        <span>INBOUND (N8N AGENTS)</span>
                                        <span className="text-red-400">500 req/sec</span>
                                    </div>
                                    <div className="h-12 flex items-end gap-1">
                                        {[...Array(20)].map((_, i) => (
                                            <div key={i} className="w-full bg-red-500/20 rounded-sm animate-pulse" style={{ height: `${Math.random() * 80 + 20}%`, animationDuration: `${Math.random() * 0.5 + 0.5}s` }}></div>
                                        ))}
                                    </div>
                                </div>
                                <div className="h-px bg-zinc-800 w-full"></div>
                                <div>
                                    <div className="flex justify-between text-xs font-mono text-zinc-500 mb-2">
                                        <span>OUTBOUND (SUPABASE)</span>
                                        <span className="text-green-400">50 req/sec (Safe)</span>
                                    </div>
                                    <div className="h-12 flex items-end gap-1">
                                        {[...Array(20)].map((_, i) => (
                                            <div key={i} className="w-full bg-green-500/50 rounded-sm transition-all duration-300" style={{ height: `40%` }}></div>
                                        ))}
                                    </div>
                                </div>
                            </div>
                        </RevealOnScroll>
                    </div>
                </section>

                {/* 2. The Pipe */}
                <section className="py-24 border-t border-toro-border bg-zinc-900/10">
                    <div className="max-w-7xl mx-auto px-6 grid lg:grid-cols-2 gap-16 items-center lg:flex-row-reverse">
                        <RevealOnScroll delay={200} className="order-2 lg:order-1 relative animate-float" style={{ animationDelay: '1s' }}>
                            <DashboardDemo />
                        </RevealOnScroll>
                        <RevealOnScroll className="order-1 lg:order-2">
                            <div className="w-12 h-12 bg-zinc-800 rounded-lg flex items-center justify-center mb-6 text-blue-500 animate-float" style={{ animationDelay: '0.5s' }}>
                                <Icons.Terminal className="w-6 h-6" />
                            </div>
                            <h2 className="text-4xl font-bold mb-6">The Pipe. <br /><span className="text-zinc-500">Fix. Replay. Done.</span></h2>
                            <p className="text-lg text-zinc-400 mb-8 leading-relaxed">
                                Forget Ngrok. Toro gives you a persistent tunnel that stores every webhook. If your server crashes or your code has a bug, simply fix the issue and click <strong>Replay</strong> to re-run the exact event against your destination of choice.
                            </p>
                            <ul className="space-y-4 text-zinc-300 mb-8">
                                <li className="flex items-center gap-3">
                                    <Icons.RotateCw className="w-5 h-5 text-blue-400" />
                                    <span>One-click replay for failed events</span>
                                </li>
                                <li className="flex items-center gap-3">
                                    <Icons.Brain className="w-5 h-5 text-purple-400" />
                                    <span>AI Root Cause Analysis</span>
                                </li>
                                <li className="flex items-center gap-3">
                                    <Icons.Check className="w-5 h-5 text-green-500" />
                                    <span>Persistent tunnels (no random URLs)</span>
                                </li>
                            </ul>
                            <button className="text-green-400 hover:text-green-300 font-mono text-sm flex items-center gap-2 group">
                                Read the CLI docs <span className="group-hover:translate-x-1 transition-transform">-&gt;</span>
                            </button>
                        </RevealOnScroll>
                    </div>
                </section>

                {/* 3. The Concierge Suite */}
                <section className="py-24 border-t border-toro-border">
                    <div className="max-w-7xl mx-auto px-6">
                        <SectionTitle badge="THE CONCIERGE">Automated Financial Services</SectionTitle>

                        {/* Part A: QuickBooks Concierge */}
                        <div className="grid lg:grid-cols-2 gap-16 items-center mb-32">
                            <RevealOnScroll>
                                <div className="w-12 h-12 bg-zinc-800 rounded-lg flex items-center justify-center mb-6 text-green-500 animate-float">
                                    <Icons.FileText className="w-6 h-6" />
                                </div>
                                <h3 className="text-3xl font-bold mb-4">QuickBooks Concierge. <br /><span className="text-zinc-500">POS to Accounting.</span></h3>
                                <p className="text-lg text-zinc-400 mb-6 leading-relaxed">
                                    Stop acting like an accountant. We accept raw sales receipts from your app or POS, normalize them, transform them into accounting semantics, and post them directly to your customer's QuickBooks.
                                </p>
                                <ul className="space-y-3 text-zinc-300">
                                    <li className="flex items-center gap-3">
                                        <Icons.Check className="w-5 h-5 text-green-500" />
                                        <span>Raw POS Data &rarr; Accounting Entries</span>
                                    </li>
                                    <li className="flex items-center gap-3">
                                        <Icons.Check className="w-5 h-5 text-green-500" />
                                        <span>Automatic Schema & Token Management</span>
                                    </li>
                                    <li className="flex items-center gap-3">
                                        <Icons.Check className="w-5 h-5 text-green-500" />
                                        <span>Error Handling & Instant Retries</span>
                                    </li>
                                </ul>
                            </RevealOnScroll>
                            <RevealOnScroll delay={200} className="bg-[#0A0A0A] border border-toro-border rounded-xl p-8 shadow-2xl relative">
                                <div className="flex items-center justify-between text-xs font-mono mb-8 opacity-50">
                                    <span>COFFEE SHOP POS</span>
                                    <span>&rarr;</span>
                                    <span>QUICKBOOKS ONLINE</span>
                                </div>
                                <div className="space-y-4">
                                    <div className="p-3 bg-zinc-900 border border-dashed border-zinc-700 rounded text-xs text-zinc-500 font-mono">
                                        {`{ "item": "Latte", "price": 5.00, "tax": 0.45 }`}
                                    </div>
                                    <div className="flex justify-center flex-col items-center gap-1">
                                        <div className="h-4 w-px bg-zinc-700"></div>
                                        <div className="flex items-center gap-2">
                                            <Icons.Brain className="w-5 h-5 text-green-500 animate-pulse" />
                                            <span className="text-[10px] text-green-400 font-mono tracking-widest">SEMANTIC MAPPING</span>
                                        </div>
                                        <div className="h-4 w-px bg-zinc-700"></div>
                                    </div>
                                    <div className="p-4 bg-green-900/10 border border-green-500/30 rounded text-xs font-mono text-green-400 relative overflow-hidden">
                                        <div className="absolute top-0 right-0 p-1 bg-green-500/20 rounded-bl text-[8px] px-2 text-green-300">POSTED</div>
                                        <div>Category: <span className="text-white">Sales:Beverages</span></div>
                                        <div>Credit: <span className="text-white">Income</span> ($5.00)</div>
                                        <div>Debit: <span className="text-white">Undeposited Funds</span></div>
                                    </div>
                                </div>
                            </RevealOnScroll>
                        </div>

                        {/* Part B: Plaid Concierge */}
                        <div className="grid lg:grid-cols-2 gap-16 items-center lg:flex-row-reverse">
                            <RevealOnScroll delay={100} className="order-2 lg:order-1 relative">
                                <div className="bg-[#0A0A0A] border border-toro-border rounded-xl p-8 shadow-2xl">
                                    <div className="flex items-center justify-between mb-6">
                                        <div className="flex items-center gap-2">
                                            <div className="w-2 h-2 rounded-full bg-blue-500 animate-pulse"></div>
                                            <span className="text-xs font-mono text-blue-400">WEBHOOK RECEIVED</span>
                                        </div>
                                        <span className="text-xs font-mono text-zinc-600">Syncing...</span>
                                    </div>
                                    <div className="space-y-3 font-mono text-xs">
                                        <div className="flex items-center gap-3 text-zinc-500">
                                            <Icons.Activity className="w-4 h-4" />
                                            <span>Fetching transaction details...</span>
                                        </div>
                                        <div className="flex items-center gap-3 text-purple-400">
                                            <Icons.Brain className="w-4 h-4" />
                                            <span>Cleaning: "UBER *RIDE" &rarr; "Uber"</span>
                                        </div>
                                        <div className="flex items-center gap-3 text-green-500">
                                            <Icons.Server className="w-4 h-4" />
                                            <span>INSERT INTO transactions (Safe)</span>
                                        </div>
                                        <div className="p-2 mt-4 bg-zinc-900 border border-green-500/50 rounded text-[10px] text-green-400 font-bold italic shadow-[0_0_15px_rgba(74,222,128,0.4)] animate-pulse">
                                            "We handle pagination, cursors, and updates."
                                        </div>
                                    </div>
                                </div>
                            </RevealOnScroll>
                            <RevealOnScroll className="order-1 lg:order-2">
                                <div className="w-12 h-12 bg-zinc-800 rounded-lg flex items-center justify-center mb-6 text-blue-500 animate-float">
                                    <Icons.Activity className="w-6 h-6" />
                                </div>
                                <h3 className="text-3xl font-bold mb-4">Plaid Concierge. <br /><span className="text-zinc-500">Data Sync, Resolved.</span></h3>
                                <p className="text-lg text-zinc-400 mb-6 leading-relaxed">
                                    If a Plaid webhook says something changed, we handle it. We fetch the data, clean up messy merchant names using AI, and sync it directly to your database. No more missing transactions.
                                </p>
                                <ul className="space-y-3 text-zinc-300">
                                    <li className="flex items-center gap-3">
                                        <Icons.Check className="w-5 h-5 text-blue-500" />
                                        <span>Webhook Listening & Verification</span>
                                    </li>
                                    <li className="flex items-center gap-3">
                                        <Icons.Check className="w-5 h-5 text-blue-500" />
                                        <span>AI Merchant Cleaning</span>
                                    </li>
                                    <li className="flex items-center gap-3">
                                        <Icons.Check className="w-5 h-5 text-blue-500" />
                                        <span>Direct DB Sync (Supabase, Postgres)</span>
                                    </li>
                                </ul>
                            </RevealOnScroll>
                        </div>
                    </div>
                </section>
            </div>

            {/* Architecture Section */}
            <section className="py-32 border-t border-toro-border bg-[#030303] overflow-hidden">
                <div className="max-w-7xl mx-auto px-6 text-center">
                    <SectionTitle badge="ARCHITECTURE">How it fits your stack</SectionTitle>

                    <div className="mt-16">
                        <ArchitectureRow
                            title="The Airbag"
                            source="N8N / Agents"
                            dest="Supabase / Postgres"
                            toroAction="BUFFER & QUEUE"
                            icon={Icons.Shield}
                            delay={0}
                        />

                        <ArchitectureRow
                            title="Concierge (QB)"
                            source="App / POS"
                            dest="QuickBooks Online"
                            toroAction="NORMALIZE & POST"
                            icon={Icons.FileText}
                            delay={100}
                        />

                        <ArchitectureRow
                            title="The Pipe"
                            source="Stripe / Twilio"
                            dest="Localhost:3000"
                            toroAction="TUNNEL & ANALYZE"
                            icon={Icons.Terminal}
                            delay={200}
                        />

                        <ArchitectureRow
                            title="Concierge (Plaid)"
                            source="Plaid / Banks"
                            dest="Your Backend API"
                            toroAction="SYNC & CLEAN"
                            icon={Icons.Activity}
                            delay={300}
                        />
                    </div>
                </div>
            </section>

            {/* Pricing Section */}
            <section id="pricing" className="py-24 border-t border-toro-border relative overflow-hidden bg-[#080808]">
                <div className="absolute inset-0 grid-bg opacity-[0.05]"></div>
                <div className="max-w-7xl mx-auto px-6 relative z-10">
                    <SectionTitle badge="RELIABLE INFRASTRUCTURE">Pricing Built for Reliability</SectionTitle>
                    <p className="text-center text-zinc-400 max-w-2xl mx-auto -mt-10 mb-16 text-lg">
                        Choose the level of control and reliability your infra requires.
                    </p>

                    <div className="grid lg:grid-cols-3 gap-6 mt-8">
                        {/* Tier 1: Toro Ingest */}
                        <RevealOnScroll delay={0} className="p-8 rounded-xl bg-zinc-900/30 border border-zinc-800 hover:border-zinc-700 transition-all flex flex-col group hover:bg-zinc-900/50">
                            <div className="mb-6 border-b border-zinc-800 pb-6">
                                <h3 className="text-xl font-bold text-white mb-2">Toro Ingest</h3>
                                <div className="text-zinc-500 text-sm font-medium uppercase tracking-wide">Webhook Reliability & Data Safety</div>
                            </div>

                            <div className="mb-6">
                                <div className="text-4xl font-bold text-white mb-2">$499<span className="text-lg text-zinc-500 font-normal">/mo</span></div>
                                <p className="text-zinc-400 text-sm">Scales by event volume</p>
                            </div>

                            <div className="mb-8">
                                <div className="text-xs font-bold text-zinc-300 uppercase tracking-widest mb-4">Designed For</div>
                                <p className="text-sm text-zinc-400 leading-relaxed">
                                    SaaS companies ingesting high-volume financial webhooks. Teams tired of dropped, malformed, or unreplayable events.
                                </p>
                            </div>

                            <ul className="space-y-4 mb-8 flex-1 text-sm text-zinc-300">
                                <li className="flex items-start gap-3"><Icons.Check className="w-5 h-5 text-zinc-500 shrink-0" /> <span>Unlimited webhook endpoints</span></li>
                                <li className="flex items-start gap-3"><Icons.Check className="w-5 h-5 text-zinc-500 shrink-0" /> <span>Unlimited tunnels</span></li>
                                <li className="flex items-start gap-3"><Icons.Check className="w-5 h-5 text-zinc-500 shrink-0" /> <span>Guaranteed event ingestion</span></li>
                                <li className="flex items-start gap-3"><Icons.Check className="w-5 h-5 text-zinc-500 shrink-0" /> <span>Event replay & reprocessing</span></li>
                                <li className="flex items-start gap-3"><Icons.Check className="w-5 h-5 text-zinc-500 shrink-0" /> <span>AI-assisted payload repair</span></li>
                                <li className="flex items-start gap-3"><Icons.Check className="w-5 h-5 text-zinc-500 shrink-0" /> <span>Audit-ready event history</span></li>
                                <li className="flex items-start gap-3"><Icons.Check className="w-5 h-5 text-zinc-500 shrink-0" /> <span>30-day log retention</span></li>
                            </ul>

                            <button onClick={() => openContact('Toro Ingest', '$499/mo')} className="w-full py-4 rounded-lg border border-zinc-700 text-white font-semibold hover:bg-zinc-800 transition-colors uppercase text-xs tracking-widest">Start with Ingestion</button>
                        </RevealOnScroll>

                        {/* Tier 2: Toro Sync */}
                        <RevealOnScroll delay={100} className="p-8 rounded-xl bg-[#0A0A0A] border border-green-500/30 relative flex flex-col shadow-[0_0_40px_rgba(34,197,94,0.05)] transform md:-translate-y-4 md:border-t-4 md:border-t-green-500">
                            <div className="mb-6 border-b border-zinc-800 pb-6">
                                <h3 className="text-xl font-bold text-white mb-2">Toro Sync</h3>
                                <div className="text-green-400 text-sm font-medium uppercase tracking-wide">Managed Financial Integrations</div>
                            </div>

                            <div className="mb-6">
                                <div className="text-4xl font-bold text-white mb-2">$1,500<span className="text-lg text-zinc-500 font-normal">/mo</span></div>
                                <p className="text-zinc-400 text-sm">Per integration (e.g. Plaid, QuickBooks)</p>
                            </div>

                            <div className="mb-8">
                                <div className="text-xs font-bold text-zinc-300 uppercase tracking-widest mb-4">Designed For</div>
                                <p className="text-sm text-zinc-400 leading-relaxed">
                                    Teams that do not want to build or maintain complex financial integrations where correctness is critical.
                                </p>
                            </div>

                            <ul className="space-y-4 mb-8 flex-1 text-sm text-zinc-300">
                                <li className="flex items-start gap-3"><span className="text-green-500 font-bold shrink-0">+</span> <span>Everything in Ingest</span></li>
                                <li className="flex items-start gap-3"><Icons.Check className="w-5 h-5 text-green-500 shrink-0" /> <span>Managed Plaid / QuickBooks Sync</span></li>
                                <li className="flex items-start gap-3"><Icons.Check className="w-5 h-5 text-green-500 shrink-0" /> <span>Token lifecycle management</span></li>
                                <li className="flex items-start gap-3"><Icons.Check className="w-5 h-5 text-green-500 shrink-0" /> <span>Automatic retries with backoff</span></li>
                                <li className="flex items-start gap-3"><Icons.Check className="w-5 h-5 text-green-500 shrink-0" /> <span>Schema normalization</span></li>
                                <li className="flex items-start gap-3"><Icons.Check className="w-5 h-5 text-green-500 shrink-0" /> <span>Integration health monitoring</span></li>
                            </ul>

                            <button onClick={() => openContact('Toro Sync', '$1,500/mo')} className="w-full py-4 rounded-lg bg-green-600 text-white font-semibold hover:bg-green-500 transition-colors shadow-lg shadow-green-900/20 uppercase text-xs tracking-widest">Stop Maintaining Integrations</button>
                        </RevealOnScroll>

                        {/* Tier 3: Ledger Intelligence */}
                        <RevealOnScroll delay={200} className="p-8 rounded-xl bg-zinc-900/30 border border-zinc-800 hover:border-zinc-700 transition-all flex flex-col group hover:bg-zinc-900/50">
                            <div className="mb-6 border-b border-zinc-800 pb-6">
                                <h3 className="text-xl font-bold text-white mb-2">Ledger Intelligence</h3>
                                <div className="text-purple-400 text-sm font-medium uppercase tracking-wide">Financial Truth & Reconciliation</div>
                            </div>

                            <div className="mb-6">
                                <div className="text-4xl font-bold text-white mb-2">$5,000<span className="text-lg text-zinc-500 font-normal">/mo</span></div>
                                <p className="text-zinc-400 text-sm">Custom enterprise pricing available</p>
                            </div>

                            <div className="mb-8">
                                <div className="text-xs font-bold text-zinc-300 uppercase tracking-widest mb-4">Designed For</div>
                                <p className="text-sm text-zinc-400 leading-relaxed">
                                    CFO-grade financial visibility. Companies preparing for audits, due diligence, or massive scale.
                                </p>
                            </div>

                            <ul className="space-y-4 mb-8 flex-1 text-sm text-zinc-300">
                                <li className="flex items-start gap-3"><span className="text-purple-400 font-bold shrink-0">+</span> <span>Everything in Sync</span></li>
                                <li className="flex items-start gap-3"><Icons.Check className="w-5 h-5 text-purple-400 shrink-0" /> <span>Unified financial transaction ledger</span></li>
                                <li className="flex items-start gap-3"><Icons.Check className="w-5 h-5 text-purple-400 shrink-0" /> <span>Cross-provider reconciliation</span></li>
                                <li className="flex items-start gap-3"><Icons.Check className="w-5 h-5 text-purple-400 shrink-0" /> <span>Missing/Duplicate transaction detection</span></li>
                                <li className="flex items-start gap-3"><Icons.Check className="w-5 h-5 text-purple-400 shrink-0" /> <span>AI-assisted discrepancy analysis</span></li>
                                <li className="flex items-start gap-3"><Icons.Check className="w-5 h-5 text-purple-400 shrink-0" /> <span>Priority support & SLA</span></li>
                            </ul>

                            <button onClick={() => openContact('Ledger Intelligence', '$5,000/mo')} className="w-full py-4 rounded-lg border border-zinc-700 text-white font-semibold hover:bg-zinc-800 transition-colors uppercase text-xs tracking-widest">Start with Ledger</button>
                        </RevealOnScroll>
                    </div>

                    {/* Enterprise Block */}
                    <RevealOnScroll delay={300} className="mt-12 p-8 rounded-xl bg-zinc-900/20 border border-zinc-800 flex flex-col md:flex-row items-center justify-between gap-8">
                        <div>
                            <h3 className="text-2xl font-bold text-white mb-2">Enterprise</h3>
                            <p className="text-zinc-400 mb-4">Custom Infrastructure & Compliance</p>
                            <div className="flex flex-wrap gap-4 text-sm text-zinc-500">
                                <span className="flex items-center gap-2"><Icons.Check className="w-4 h-4" /> Dedicated Infrastructure</span>
                                <span className="flex items-center gap-2"><Icons.Check className="w-4 h-4" /> On-prem Deployment</span>
                                <span className="flex items-center gap-2"><Icons.Check className="w-4 h-4" /> SOC Audit Logs</span>
                                <span className="flex items-center gap-2"><Icons.Check className="w-4 h-4" /> Architecture Reviews</span>
                            </div>
                        </div>
                        <button onClick={() => setIsContactOpen(true)} className="px-8 py-3 rounded-lg bg-white text-black font-bold hover:bg-zinc-200 transition-colors whitespace-nowrap">Talk to Sales</button>
                    </RevealOnScroll>

                    <div className="mt-12 text-center border-t border-zinc-900 pt-8">
                        <p className="text-zinc-600 text-xs uppercase tracking-widest font-mono">
                            Toro is infrastructure. Pricing reflects reliability, durability, and operational risk reduction.
                        </p>
                    </div>
                </div>
            </section>

            {/* FAQ Section */}
            <section id="faq" className="py-24 border-t border-toro-border">
                <div className="max-w-3xl mx-auto px-6">
                    <SectionTitle badge="FAQ">Common Questions</SectionTitle>
                    <div className="space-y-6">
                        {[
                            { q: "Do you replace Supabase?", a: "No. We protect Supabase. We sit in front of it to manage connection pooling and queueing so your low-code tools don't crash it." },
                            { q: "How does the CLI help my workflow?", a: "It keeps you in the flow state. Instead of deploying to the cloud just to test a webhook, the CLI pipes live data directly to your local N8N instance or AI agent. You get instant feedback and can fix bugs in real-time without waiting for a deployment pipeline." },
                            { q: "Is this secure?", a: "Yes. We encrypt all payloads at rest. For Plaid integration, we use AES-256 for token storage. We are SOC-2 Type 1 compliant (in progress)." },
                            { q: "Can I self host?", a: "Not yet. But the CLI is open source. We plan to offer a self-hosted Docker container for Enterprise plans later this year." },
                            { q: "What does it cost?", a: "During beta, it's free. At launch, we'll have a generous free tier for developers, with Team plans starting at $99/mo." }
                        ].map((item, i) => (
                            <RevealOnScroll key={i} delay={i * 100} className="p-6 rounded-xl bg-zinc-900/20 border border-zinc-800 hover:border-toro-border hover:bg-zinc-900/40 transition-colors cursor-default">
                                <h3 className="font-bold mb-2">{item.q}</h3>
                                <p className="text-zinc-400 text-sm leading-relaxed">{item.a}</p>
                            </RevealOnScroll>
                        ))}
                    </div>
                </div>
            </section>

            {/* CTA Section */}
            <section id="join" className="py-32 border-t border-toro-border bg-gradient-to-b from-zinc-900/20 to-black text-center relative overflow-hidden">
                <div className="absolute inset-0 grid-bg opacity-[0.05] animate-grid-flow"></div>
                <div className="max-w-2xl mx-auto px-6 relative z-10">
                    <RevealOnScroll>
                        <h2 className="text-4xl font-bold mb-6">Ready to stop debugging?</h2>
                        <p className="text-zinc-400 mb-10 text-lg">
                            Join the waitlist to get early access to the CLI and Cloud Dashboard.
                        </p>
                        <div className="flex justify-center">
                            <WaitlistForm id="footer-form" />
                        </div>
                    </RevealOnScroll>
                </div>
            </section>

            {/* Footer */}
            <footer className="py-12 text-center text-zinc-600 text-sm border-t border-toro-border bg-[#050505]">
                <div className="flex justify-center gap-6 mb-8 font-medium">
                    <a href="#" className="hover:text-zinc-400 transition-colors">Twitter</a>
                    <a href="#" className="hover:text-zinc-400 transition-colors">GitHub</a>
                    <a href="#" className="hover:text-zinc-400 transition-colors">Docs</a>
                </div>
                <p>© 2026 Toro Systems. Built for stubborn builders.</p>
            </footer>

            <SalesContactModal isOpen={isContactOpen} tier={selectedTier.name} price={selectedTier.price} onClose={() => setIsContactOpen(false)} />
        </div>
    );
};

export default LandingPage;
