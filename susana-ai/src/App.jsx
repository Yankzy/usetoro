import React, { useState, useEffect } from 'react';
import { motion, AnimatePresence } from 'framer-motion';
import {
  Menu, Sparkles, Phone, Calendar, FileText, Bot, User, CheckCircle2,
  Award, BrainCircuit, Droplets, Wind, Zap, Home, Leaf, Plug, Settings2,
  Power, PhoneCall, MapPin, Receipt, Languages, MessageSquare, ShieldCheck,
  Check, X, Loader2, Send, PlayCircle, ArrowRight, Mic, Users
} from 'lucide-react';

// --- Constants & Data ---

const HERO_IMAGES = [
  "https://images.unsplash.com/photo-1504307651254-35680f356dfd?ixlib=rb-4.0.3&auto=format&fit=crop&w=2000&q=80",
  "https://images.unsplash.com/photo-1581092160562-40aa08e78837?ixlib=rb-4.0.3&auto=format&fit=crop&w=2000&q=80",
  "https://images.unsplash.com/photo-1581141849291-1125c7b692b5?ixlib=rb-4.0.3&auto=format&fit=crop&w=2000&q=80",
  "https://images.unsplash.com/photo-lPU9syXYyVs?ixlib=rb-4.0.3&auto=format&fit=crop&w=2000&q=80",
  "https://images.unsplash.com/photo-1504328345606-18bbc8c9d7d1?ixlib=rb-4.0.3&auto=format&fit=crop&w=2000&q=80"
];

const FEATURES = [
  {
    id: 'call-answering',
    icon: Mic,
    title: '24/7 Call Answering',
    description: 'Never send a customer to voicemail again. Susana answers instantly.',
    content: (
      <div className="text-left w-full">
        <div className="flex items-center gap-3 mb-6">
          <motion.div
            animate={{ scale: [1, 1.1, 1] }}
            transition={{ repeat: Infinity, duration: 2 }}
            className="w-10 h-10 rounded-full bg-blue-600 flex items-center justify-center"
          >
            <Mic className="w-5 h-5 text-white" />
          </motion.div>
          <div>
            <h4 className="font-bold text-white text-lg">Voice AI Capabilities</h4>
            <p className="text-blue-400 text-sm">Natural, human-like conversations.</p>
          </div>
        </div>
        <div className="space-y-4">
          <div className="bg-[#151828]/80 p-4 rounded-xl border border-white/5">
            <p className="text-sm text-slate-300">"Susana handled an emergency plumbing call at 2 AM on a Sunday, gathered the leak details, and put it on my Monday morning board."</p>
          </div>
          <ul className="space-y-3 text-sm text-slate-400">
            {['Answers on the first ring, every time.', 'Collects name, address, and job details.', 'Distinguishes emergencies from standard quotes.'].map((text, i) => (
              <motion.li
                initial={{ opacity: 0, x: 20 }} animate={{ opacity: 1, x: 0 }} transition={{ delay: i * 0.15 }}
                key={i} className="flex items-center gap-2"
              >
                <Check className="w-4 h-4 text-blue-500" /> {text}
              </motion.li>
            ))}
          </ul>
        </div>
      </div>
    )
  },
  {
    id: 'smart-scheduling',
    icon: Calendar,
    title: 'Smart Scheduling',
    description: 'Books jobs directly into your calendar without double-booking.',
    content: (
      <div className="text-left w-full">
        <div className="flex items-center gap-3 mb-6">
          <div className="w-10 h-10 rounded-full bg-purple-600 flex items-center justify-center">
            <Calendar className="w-5 h-5 text-white" />
          </div>
          <div>
            <h4 className="font-bold text-white text-lg">Direct Calendar Integration</h4>
            <p className="text-purple-400 text-sm">Reads and writes to your dispatch board.</p>
          </div>
        </div>
        <div className="w-full bg-[#0B0D17] border border-white/10 rounded-lg overflow-hidden">
          <div className="bg-[#151828] px-4 py-2 flex justify-between items-center text-xs text-slate-400 border-b border-white/5">
            <span>Tuesday, Oct 24</span>
            <span>ServiceTitan Sync: Active</span>
          </div>
          <div className="p-4 space-y-2 relative">
            <div className="flex gap-4 items-center">
              <div className="text-xs text-slate-500 w-12 text-right">9:00 AM</div>
              <div className="flex-1 bg-slate-800/50 p-2 rounded text-xs border border-slate-700">Blocked (Tech: Mike)</div>
            </div>
            <motion.div initial={{ opacity: 0, scale: 0.95 }} animate={{ opacity: 1, scale: 1 }} transition={{ delay: 0.2 }} className="flex gap-4 items-center">
              <div className="text-xs text-slate-500 w-12 text-right">10:30 AM</div>
              <div className="flex-1 bg-blue-600/30 p-2 rounded text-xs border border-blue-500/50 text-blue-200 shadow-[0_0_10px_rgba(37,99,235,0.2)]">
                <span className="animate-pulse inline-block w-2 h-2 rounded-full bg-blue-400 mr-1"></span>
                Just booked by Susana: AC Tune-up
              </div>
            </motion.div>
            <div className="flex gap-4 items-center">
              <div className="text-xs text-slate-500 w-12 text-right">1:00 PM</div>
              <div className="flex-1 border border-dashed border-white/10 p-2 rounded text-xs text-slate-600">Available</div>
            </div>
          </div>
        </div>
      </div>
    )
  },
  {
    id: 'invoicing',
    icon: Receipt,
    title: 'Automated Estimates',
    description: 'Generates quotes instantly based on your company pricebook.',
    content: (
      <div className="text-left w-full">
        <div className="flex items-center gap-3 mb-6">
          <div className="w-10 h-10 rounded-full bg-emerald-600 flex items-center justify-center">
            <Receipt className="w-5 h-5 text-white" />
          </div>
          <div>
            <h4 className="font-bold text-white text-lg">Instant Quote Generation</h4>
            <p className="text-emerald-400 text-sm">Turns descriptions into professional estimates.</p>
          </div>
        </div>
        <motion.div
          initial={{ rotate: 5, y: 20 }}
          animate={{ rotate: 0, y: 0 }}
          transition={{ type: "spring", stiffness: 100 }}
          className="bg-white text-slate-900 rounded-lg p-5 shadow-lg relative"
        >
          <div className="flex justify-between items-start border-b pb-3 mb-3">
            <div>
              <h5 className="font-bold">ESTIMATE #1042</h5>
              <p className="text-xs text-slate-500">Generated automatically via SMS text</p>
            </div>
            <div className="text-right">
              <p className="font-bold text-lg">$285.00</p>
            </div>
          </div>
          <div className="text-sm">
            <p className="font-semibold">Diagnostic Fee (Standard)</p>
            <p className="text-xs text-slate-600 mb-2">Includes trip charge and up to 1 hr troubleshooting.</p>
            <button className="w-full mt-3 bg-slate-900 text-white py-2 rounded text-xs font-bold hover:bg-slate-800 transition-colors">
              Customer: Tap to Approve
            </button>
          </div>
        </motion.div>
      </div>
    )
  }
];

const LOGOS = [
  { icon: Droplets, name: "Apex Plumbing", color: "text-blue-400" },
  { icon: Wind, name: "Breeze HVAC", color: "text-sky-400" },
  { icon: Zap, name: "Volt Electric", color: "text-yellow-400" },
  { icon: Home, name: "Summit Roofing", color: "text-emerald-400" },
  { icon: Leaf, name: "Elite Landscaping", color: "text-green-400" }
];

// --- Shared Framer Motion Variants ---
const fadeUpVar = {
  hidden: { opacity: 0, y: 30 },
  visible: { opacity: 1, y: 0, transition: { duration: 0.6, ease: "easeOut" } }
};

const staggerContainer = {
  hidden: { opacity: 0 },
  visible: {
    opacity: 1,
    transition: { staggerChildren: 0.15 }
  }
};

const popInVar = {
  hidden: { opacity: 0, scale: 0.8 },
  visible: { opacity: 1, scale: 1, transition: { type: "spring", stiffness: 100 } }
};

// --- Main Application Component ---
export default function App() {
  const [isScrolled, setIsScrolled] = useState(false);
  const [isMobileMenuOpen, setIsMobileMenuOpen] = useState(false);
  const [currentSlide, setCurrentSlide] = useState(0);
  const [activeFeatureTab, setActiveFeatureTab] = useState(FEATURES[0].id);

  // Stripe Checkout State
  const [checkoutLoading, setCheckoutLoading] = useState(null);

  // Form State
  const [isFormProcessing, setIsFormProcessing] = useState(false);
  const [isFormSuccess, setIsFormSuccess] = useState(false);

  // Inject Tailwind CDN Fallback if local build is broken
  useEffect(() => {
    if (!document.getElementById('tailwind-cdn')) {
      const script = document.createElement('script');
      script.id = 'tailwind-cdn';
      script.src = 'https://cdn.tailwindcss.com';
      document.head.appendChild(script);
    }
  }, []);

  // Scroll Listener
  useEffect(() => {
    const handleScroll = () => setIsScrolled(window.scrollY > 20);
    window.addEventListener('scroll', handleScroll);
    return () => window.removeEventListener('scroll', handleScroll);
  }, []);

  // Hero Slider Timer
  useEffect(() => {
    const timer = setInterval(() => setCurrentSlide(prev => (prev + 1) % HERO_IMAGES.length), 6000);
    return () => clearInterval(timer);
  }, []);

  // Stripe Checkout Handler
  const handleStripeCheckout = (planName, monthlyPrice, includesSetupFee) => {
    setCheckoutLoading(planName);

    // Simulating the backend API call to create a Stripe Checkout Session
    setTimeout(() => {
      // In a real production environment, you would execute:
      // const response = await fetch('/api/create-checkout-session', { ... })
      // const session = await response.json();
      // window.location.href = session.url;

      // For the interactive preview demo, we reset the loading state after 2.5s
      setCheckoutLoading(null);
    }, 2500);
  };

  // Contact Form Handler
  const handleContactSubmit = async (e) => {
    e.preventDefault();
    setIsFormProcessing(true);

    // Automatically gather all inputs that have a 'name' attribute
    const formData = new FormData(e.target);
    const payload = Object.fromEntries(formData.entries());

    try {
      const response = await fetch('https://prime-legible-turkey.ngrok-free.app/api/forms/susanaai.com', {
        method: 'POST',
        headers: {
          'Content-Type': 'application/json',
        },
        body: JSON.stringify(payload),
      });

      if (response.ok) {
        setIsFormSuccess(true);
        e.target.reset();
        setTimeout(() => setIsFormSuccess(false), 5000);
      } else {
        console.error("Form submission failed with status:", response.status);
        // Optional: You could set an error state here to show the user
      }
    } catch (error) {
      console.error("Network error during form submission:", error);
    } finally {
      setIsFormProcessing(false);
    }
  };

  return (
    <div className="min-h-screen bg-[#0B0D17] text-slate-50 font-sans overflow-x-hidden selection:bg-blue-500 selection:text-white">

      {/* Global Styles */}
      <style dangerouslySetInnerHTML={{
        __html: `
                .glass-card {
                    background: linear-gradient(145deg, rgba(30, 35, 55, 0.8) 0%, rgba(21, 24, 40, 0.9) 100%);
                    border: 1px solid rgba(255, 255, 255, 0.05);
                    box-shadow: 0 8px 32px 0 rgba(0, 0, 0, 0.3);
                }
                .text-gradient {
                    background: linear-gradient(to right, #60a5fa, #a78bfa);
                    -webkit-background-clip: text;
                    -webkit-text-fill-color: transparent;
                    background-clip: text;
                }
            `}} />

      {/* --- Navbar --- */}
      <nav className={`fixed top-0 w-full z-50 border-b transition-all duration-300 ${isScrolled ? 'bg-[#0B0D17]/80 backdrop-blur-lg border-white/10 shadow-lg' : 'bg-transparent border-white/5 backdrop-blur-sm'}`}>
        <div className="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8">
          <div className="flex justify-between items-center h-20">
            <motion.a whileHover={{ scale: 1.05 }} whileTap={{ scale: 0.95 }} href="#" className="flex items-center">
              <div className="px-3 py-1.5 rounded-lg bg-gradient-to-r from-blue-600 to-purple-600 shadow-lg shadow-blue-500/20 border border-white/10 hover:shadow-blue-500/40 transition-shadow">
                <span className="font-bold text-xl tracking-tight text-white">Susana AI</span>
              </div>
            </motion.a>

            <div className="hidden md:flex space-x-8">
              <a href="#features" className="text-sm font-medium text-slate-300 hover:text-white transition-colors">Capabilities</a>
              <a href="#how-it-works" className="text-sm font-medium text-slate-300 hover:text-white transition-colors">How it Works</a>
              <a href="#integrations" className="text-sm font-medium text-slate-300 hover:text-white transition-colors">Integrations</a>
              <a href="#pricing" className="text-sm font-medium text-slate-300 hover:text-white transition-colors">Pricing</a>
            </div>

            <div className="hidden md:flex items-center gap-4">
              <a href="#contact" className="text-sm font-medium text-slate-300 hover:text-white transition-colors">Log In</a>
              <motion.a whileHover={{ scale: 1.05 }} whileTap={{ scale: 0.95 }} href="#contact" className="bg-white text-[#0B0D17] px-5 py-2.5 rounded-full text-sm font-semibold shadow-[0_0_15px_rgba(255,255,255,0.3)]">
                Hire Susana
              </motion.a>
            </div>

            <div className="md:hidden flex items-center">
              <button className="text-slate-300 hover:text-white focus:outline-none" onClick={() => setIsMobileMenuOpen(!isMobileMenuOpen)}>
                {isMobileMenuOpen ? <X className="w-6 h-6" /> : <Menu className="w-6 h-6" />}
              </button>
            </div>
          </div>
        </div>

        {/* Mobile Menu */}
        <AnimatePresence>
          {isMobileMenuOpen && (
            <motion.div
              initial={{ opacity: 0, y: -10 }} animate={{ opacity: 1, y: 0 }} exit={{ opacity: 0, y: -10 }}
              className="md:hidden bg-[#0B0D17]/95 backdrop-blur-xl border-t border-white/10 absolute top-20 left-0 w-full shadow-2xl"
            >
              <div className="px-4 pt-2 pb-6 space-y-1">
                <a href="#features" onClick={() => setIsMobileMenuOpen(false)} className="block px-3 py-3 text-base font-medium text-slate-300 hover:text-white hover:bg-white/5 rounded-md">Capabilities</a>
                <a href="#how-it-works" onClick={() => setIsMobileMenuOpen(false)} className="block px-3 py-3 text-base font-medium text-slate-300 hover:text-white hover:bg-white/5 rounded-md">How it Works</a>
                <a href="#pricing" onClick={() => setIsMobileMenuOpen(false)} className="block px-3 py-3 text-base font-medium text-slate-300 hover:text-white hover:bg-white/5 rounded-md">Pricing</a>
                <a href="#contact" onClick={() => setIsMobileMenuOpen(false)} className="block mt-4 text-center bg-blue-600 text-white px-5 py-3 rounded-lg text-base font-semibold">Hire Susana</a>
              </div>
            </motion.div>
          )}
        </AnimatePresence>
      </nav>

      {/* --- Hero Section --- */}
      <section className="relative pt-28 pb-16 lg:pt-48 lg:pb-32 border-b border-white/5 w-full min-h-[650px] lg:min-h-[800px] flex items-center">

        {/* Background Slider Container */}
        <div className="absolute inset-0 z-0 overflow-hidden w-full h-full bg-[#0B0D17]">
          <div className="absolute inset-0 z-10" style={{ background: 'linear-gradient(to bottom, rgba(11,13,23,0.95) 0%, rgba(11,13,23,0.3) 30%, rgba(11,13,23,0.4) 70%, rgba(11,13,23,1) 100%)', pointerEvents: 'none' }}></div>

          {/* Glowing Orbs */}
          <div className="absolute top-0 left-1/2 -translate-x-1/2 w-full max-w-5xl h-[500px] opacity-40 pointer-events-none z-10 hidden md:block">
            <motion.div animate={{ x: [0, 30, -20, 0], y: [0, -50, 20, 0], scale: [1, 1.1, 0.9, 1] }} transition={{ duration: 7, repeat: Infinity, ease: "linear" }} className="absolute top-0 left-1/4 w-72 h-72 bg-blue-600 rounded-full mix-blend-screen filter blur-[100px]" />
            <motion.div animate={{ x: [0, -30, 20, 0], y: [0, 50, -20, 0], scale: [1, 0.9, 1.1, 1] }} transition={{ duration: 7, repeat: Infinity, ease: "linear", delay: 2 }} className="absolute top-0 right-1/4 w-72 h-72 bg-purple-600 rounded-full mix-blend-screen filter blur-[100px]" />
          </div>

          <AnimatePresence mode="popLayout">
            <motion.img
              key={currentSlide} src={HERO_IMAGES[currentSlide]}
              initial={{ opacity: 0, scale: 1.05 }} animate={{ opacity: 1, scale: 1 }} exit={{ opacity: 0 }} transition={{ duration: 1.5, ease: "easeInOut" }}
              className="absolute inset-0 w-full h-full object-cover" alt="Trade workers on site"
            />
          </AnimatePresence>
        </div>

        <div className="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 relative z-20 text-center w-full">
          <motion.div initial={{ opacity: 0, y: 20 }} animate={{ opacity: 1, y: 0 }} transition={{ duration: 0.5 }} className="inline-flex items-center gap-2 px-3 py-1 rounded-full bg-blue-500/20 border border-blue-500/30 text-blue-300 text-sm font-medium mb-6 sm:mb-8 backdrop-blur-md">
            <span className="flex h-2 w-2 rounded-full bg-blue-400 animate-pulse"></span> Meet your new favorite employee
          </motion.div>

          <motion.h1 initial={{ opacity: 0, y: 20 }} animate={{ opacity: 1, y: 0 }} transition={{ duration: 0.5, delay: 0.1 }} className="text-4xl sm:text-5xl md:text-7xl font-extrabold tracking-tight mb-4 sm:mb-6 drop-shadow-lg">
            The Autonomous Backoffice<br className="hidden md:block" />
            for <span className="text-gradient drop-shadow-lg">America's Trades</span>
          </motion.h1>

          <motion.p initial={{ opacity: 0, y: 20 }} animate={{ opacity: 1, y: 0 }} transition={{ duration: 0.5, delay: 0.2 }} className="mt-4 text-lg sm:text-xl text-slate-300 max-w-3xl mx-auto mb-8 sm:mb-10 drop-shadow-md">
            Susana AI answers calls, books appointments, dispatches technicians, and chases invoices 24/7. Built specifically for Plumbing, HVAC, Electrical, and Field Service SMBs.
          </motion.p>

          <motion.div initial={{ opacity: 0, y: 20 }} animate={{ opacity: 1, y: 0 }} transition={{ duration: 0.5, delay: 0.3 }} className="flex flex-col sm:flex-row justify-center items-center gap-3 sm:gap-4">
            <motion.a whileHover={{ scale: 1.05 }} whileTap={{ scale: 0.95 }} href="#pricing" className="w-full sm:w-auto bg-white text-[#0B0D17] px-6 py-3.5 sm:px-8 sm:py-4 rounded-full text-base font-bold shadow-[0_0_30px_rgba(255,255,255,0.2)] flex items-center justify-center gap-2">
              Build Your AI Assistant <ArrowRight className="w-4 h-4" />
            </motion.a>
            <motion.a whileHover={{ scale: 1.05 }} whileTap={{ scale: 0.95 }} href="#demo" className="w-full sm:w-auto bg-[#151828]/60 backdrop-blur-md border border-white/10 text-white px-6 py-3.5 sm:px-8 sm:py-4 rounded-full text-base font-medium flex items-center justify-center gap-2">
              <PlayCircle className="w-5 h-5" /> Watch Demo
            </motion.a>
          </motion.div>
        </div>

        {/* Floating App Preview Graphic */}
        <motion.div
          initial={{ opacity: 0, y: 40 }} animate={{ opacity: 1, y: 0 }} transition={{ duration: 0.8, delay: 0.5 }}
          className="absolute -bottom-32 sm:-bottom-48 left-1/2 -translate-x-1/2 w-full max-w-5xl px-2 sm:px-6 z-20 hidden md:block"
        >
          <motion.div
            animate={{ y: [-8, 8, -8] }} transition={{ duration: 6, repeat: Infinity, ease: "easeInOut" }}
            className="relative rounded-2xl glass-card p-3 sm:p-4 shadow-2xl backdrop-blur-xl bg-[#0B0D17]/80"
          >
            <div className="flex gap-2 mb-4 px-2 pt-2">
              <div className="w-3 h-3 rounded-full bg-rose-500/80"></div>
              <div className="w-3 h-3 rounded-full bg-amber-500/80"></div>
              <div className="w-3 h-3 rounded-full bg-emerald-500/80"></div>
            </div>

            <div className="grid grid-cols-1 lg:grid-cols-3 gap-6">
              {/* Sidebar Mock */}
              <div className="hidden lg:flex flex-col gap-4 border-r border-white/5 pr-4">
                <motion.div whileHover={{ x: 5 }} className="flex items-center gap-3 p-3 rounded-lg bg-white/10 text-white cursor-pointer">
                  <Phone className="w-5 h-5 text-blue-400" /> <span className="text-sm font-medium">Live Call Handler</span>
                </motion.div>
                <motion.div whileHover={{ x: 5, backgroundColor: "rgba(255,255,255,0.05)" }} className="flex items-center gap-3 p-3 rounded-lg text-slate-400 cursor-pointer">
                  <Calendar className="w-5 h-5" /> <span className="text-sm font-medium">Smart Dispatch</span>
                </motion.div>
                <motion.div whileHover={{ x: 5, backgroundColor: "rgba(255,255,255,0.05)" }} className="flex items-center gap-3 p-3 rounded-lg text-slate-400 cursor-pointer">
                  <FileText className="w-5 h-5" /> <span className="text-sm font-medium">Auto Invoicing</span>
                </motion.div>
              </div>

              {/* Main Chat Mock */}
              <div className="lg:col-span-2 flex flex-col gap-4 min-h-[250px]">
                <div className="flex justify-between items-center pb-2 border-b border-white/5">
                  <div className="flex items-center gap-2">
                    <div className="w-8 h-8 rounded-full bg-blue-500 flex items-center justify-center">
                      <Bot className="w-4 h-4 text-white" />
                    </div>
                    <div>
                      <h3 className="text-sm font-semibold text-white">Susana AI</h3>
                      <p className="text-xs text-blue-300">Monitoring requests</p>
                    </div>
                  </div>
                  <span className="px-2 py-1 rounded text-xs font-medium bg-emerald-500/20 text-emerald-400 border border-emerald-500/20 flex items-center gap-1">
                    <div className="w-1.5 h-1.5 rounded-full bg-emerald-400 animate-pulse"></div> Active
                  </span>
                </div>

                <div className="flex flex-col gap-4 mt-2">
                  <motion.div initial={{ opacity: 0, x: -20 }} animate={{ opacity: 1, x: 0 }} transition={{ delay: 1, type: "spring" }} className="flex items-start gap-3">
                    <div className="w-8 h-8 rounded-full bg-slate-700/80 flex items-center justify-center flex-shrink-0">
                      <User className="w-4 h-4 text-slate-300" />
                    </div>
                    <div className="bg-slate-800/80 backdrop-blur-md rounded-2xl rounded-tl-none p-3 text-sm text-slate-200 shadow-sm border border-white/5 max-w-[80%]">
                      "Hi, my AC stopped working and it's 95 degrees out. Can someone come today?"
                    </div>
                  </motion.div>

                  <motion.div initial={{ opacity: 0, x: 20 }} animate={{ opacity: 1, x: 0 }} transition={{ delay: 2.5, type: "spring" }} className="flex items-start gap-3 flex-row-reverse">
                    <div className="w-8 h-8 rounded-full bg-blue-600 flex items-center justify-center flex-shrink-0 shadow-[0_0_10px_rgba(37,99,235,0.5)]">
                      <Sparkles className="w-4 h-4 text-white" />
                    </div>
                    <div className="bg-blue-600/30 backdrop-blur-md border border-blue-500/30 rounded-2xl rounded-tr-none p-4 text-sm text-white shadow-sm max-w-[85%]">
                      <div className="flex items-center gap-2 mb-2">
                        <CheckCircle2 className="w-4 h-4 text-blue-300" />
                        <span className="font-medium">Action Taken Automatically</span>
                      </div>
                      <ul className="space-y-2 text-slate-200">
                        <motion.li initial={{ opacity: 0 }} animate={{ opacity: 1 }} transition={{ delay: 3 }} className="flex items-center gap-2"><div className="w-1 h-1 rounded-full bg-blue-400"></div> Collected info & checked ServiceTitan.</motion.li>
                        <motion.li initial={{ opacity: 0 }} animate={{ opacity: 1 }} transition={{ delay: 3.5 }} className="flex items-center gap-2"><div className="w-1 h-1 rounded-full bg-blue-400"></div> Booked emergency diagnostic for 11:30 AM.</motion.li>
                      </ul>
                    </div>
                  </motion.div>
                </div>
              </div>
            </div>
          </motion.div>
        </motion.div>
      </section>

      {/* Empty Spacer for Desktop Graphic Overlay */}
      <div className="hidden md:block h-32 sm:h-48"></div>

      {/* --- Founder Bio Section --- */}
      <section id="founder" className="pt-32 sm:pt-48 pb-16 lg:pb-24 relative bg-[#0B0D17]/50 border-t border-white/5 overflow-hidden">
        <div className="absolute top-1/2 left-0 -translate-y-1/2 w-96 h-96 bg-rose-600/10 rounded-full blur-[100px] pointer-events-none"></div>

        <div className="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 relative z-10">
          <div className="grid grid-cols-1 lg:grid-cols-2 gap-12 lg:gap-20 items-center">
            <motion.div
              initial="hidden" whileInView="visible" viewport={{ once: true, amount: 0.3 }} variants={fadeUpVar}
              className="relative max-w-md mx-auto lg:mx-0 w-full"
            >
              <div className="absolute -inset-4 bg-gradient-to-tr from-rose-500/20 to-blue-500/20 rounded-2xl blur-xl -z-10"></div>
              <motion.img whileHover={{ scale: 1.02 }} transition={{ duration: 0.5 }} src="https://images.unsplash.com/photo-1567532939604-b6b5b0db2604?auto=format&fit=crop&q=80&w=800" alt="Susana, Founder" className="rounded-2xl border border-white/10 shadow-2xl object-cover aspect-[4/5] w-full grayscale-[20%] hover:grayscale-0 transition-all duration-500" />

              <motion.div initial={{ scale: 0, opacity: 0 }} whileInView={{ scale: 1, opacity: 1 }} viewport={{ once: true }} transition={{ delay: 0.6, type: "spring" }} className="absolute -bottom-6 -right-6 lg:-right-12 glass-card p-4 rounded-xl border border-white/10 shadow-2xl">
                <div className="flex items-center gap-4">
                  <div className="w-12 h-12 rounded-full bg-rose-500/20 flex items-center justify-center text-rose-400">
                    <Award className="w-6 h-6" />
                  </div>
                  <div>
                    <p className="text-white font-extrabold text-xl">20+ Years</p>
                    <p className="text-slate-400 text-xs font-medium uppercase tracking-wider">Trade Industry</p>
                  </div>
                </div>
              </motion.div>
            </motion.div>

            <motion.div initial="hidden" whileInView="visible" viewport={{ once: true, amount: 0.3 }} variants={staggerContainer}>
              <motion.div variants={fadeUpVar} className="inline-flex items-center gap-2 px-3 py-1 rounded-full bg-rose-500/10 border border-rose-500/20 text-rose-400 text-sm font-medium mb-6">
                <BrainCircuit className="w-4 h-4" /> Meet the brains behind the bot
              </motion.div>

              <motion.h2 variants={fadeUpVar} className="text-3xl sm:text-4xl md:text-5xl font-bold mb-4 sm:mb-6 leading-tight">
                Built from <span className="text-transparent bg-clip-text bg-gradient-to-r from-rose-400 to-blue-400">two decades</span> of dirty boots and late-night dispatch.
              </motion.h2>

              <motion.div variants={fadeUpVar} className="space-y-4 sm:space-y-5 text-base sm:text-lg text-slate-300">
                <p>Hi, I'm Susana.</p>
                <p>For over 20 years, I was the human "backoffice" for dozens of HVAC, plumbing, and electrical contractors across the country. I know exactly what it's like to field a frantic 3 AM call for a burst pipe.</p>
                <p>I realized I couldn't physically clone myself to help them all—until now.</p>
                <p>I partnered with top AI engineers to essentially <strong>digitize my brain</strong>. We trained Susana AI on my exact playbooks, my empathy on the phone, and my operational strictness.</p>

                <blockquote className="p-4 mt-6 border-l-2 border-rose-500 bg-rose-500/5 rounded-r-lg text-white font-medium italic">
                  "My mission is simple: to democratize elite-level virtual assistance so that every blue-collar SMB in America can punch above its weight, win more jobs, and finally get some sleep."
                </blockquote>
              </motion.div>

              <motion.div variants={fadeUpVar} className="mt-10 flex items-center gap-4">
                <div className="w-12 h-1 bg-gradient-to-r from-rose-500 to-transparent"></div>
                <div>
                  <p className="font-bold text-white text-xl">Susana M.</p>
                  <p className="text-blue-400 text-sm font-medium">Founder & Original Virtual Assistant</p>
                </div>
              </motion.div>
            </motion.div>
          </div>
        </div>
      </section>

      {/* --- Infinite Logo Marquee --- */}
      <section className="py-10 border-y border-white/5 bg-[#151828]/50 overflow-hidden">
        <div className="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 text-center mb-6">
          <p className="text-sm font-medium text-slate-400 uppercase tracking-wider">Trusted by 500+ home service businesses</p>
        </div>
        <div className="flex whitespace-nowrap overflow-hidden w-full relative">
          {/* Left/Right Fade Masks */}
          <div className="absolute left-0 top-0 w-24 h-full bg-gradient-to-r from-[#151828] to-transparent z-10"></div>
          <div className="absolute right-0 top-0 w-24 h-full bg-gradient-to-l from-[#151828] to-transparent z-10"></div>

          <motion.div
            className="flex gap-16 min-w-max items-center opacity-60 grayscale hover:grayscale-0 transition-all duration-500"
            animate={{ x: ["0%", "-50%"] }}
            transition={{ repeat: Infinity, duration: 25, ease: "linear" }}
          >
            {/* Render twice for seamless looping */}
            {[...LOGOS, ...LOGOS].map((logo, idx) => {
              const Icon = logo.icon;
              return (
                <div key={idx} className="flex items-center gap-2 font-bold text-xl px-8">
                  <Icon className={logo.color} /> {logo.name}
                </div>
              )
            })}
          </motion.div>
        </div>
      </section>

      {/* --- Interactive Features --- */}
      <section id="features" className="py-16 lg:py-24 relative">
        <div className="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8">
          <motion.div initial="hidden" whileInView="visible" viewport={{ once: true, amount: 0.5 }} variants={fadeUpVar} className="text-center max-w-3xl mx-auto mb-10 sm:mb-16">
            <h2 className="text-3xl sm:text-4xl md:text-5xl font-bold mb-4">A complete backoffice team, <br className="hidden sm:block" /><span className="text-gradient">in one AI agent.</span></h2>
            <p className="text-base sm:text-lg text-slate-400">Stop missing calls when you're under a sink or on a roof. Susana manages the entire customer lifecycle from first ring to final invoice.</p>
          </motion.div>

          <div className="grid grid-cols-1 lg:grid-cols-12 gap-8 lg:gap-12 items-center">
            <motion.div initial="hidden" whileInView="visible" viewport={{ once: true }} variants={staggerContainer} className="lg:col-span-5 space-y-3 sm:space-y-4">
              {FEATURES.map((feature) => {
                const isActive = feature.id === activeFeatureTab;
                const Icon = feature.icon;
                return (
                  <motion.div
                    variants={fadeUpVar}
                    whileHover={{ scale: 1.02 }}
                    key={feature.id}
                    onClick={() => setActiveFeatureTab(feature.id)}
                    className={`p-4 sm:p-5 rounded-xl cursor-pointer border transition-colors duration-300 ${isActive ? 'bg-white/10 border-white/20 shadow-lg' : 'bg-transparent border-transparent hover:bg-white/5'}`}
                  >
                    <div className="flex items-start gap-3 sm:gap-4">
                      <div className={`mt-1 ${isActive ? 'text-blue-400' : 'text-slate-500'}`}>
                        <Icon className="w-5 h-5 sm:w-6 sm:h-6" />
                      </div>
                      <div>
                        <h3 className={`text-lg sm:text-xl font-bold ${isActive ? 'text-white' : 'text-slate-300'}`}>{feature.title}</h3>
                        <p className={`mt-1 text-xs sm:text-sm ${isActive ? 'text-slate-300' : 'text-slate-500'}`}>{feature.description}</p>
                      </div>
                    </div>
                  </motion.div>
                );
              })}
            </motion.div>

            <div className="lg:col-span-7">
              <motion.div initial={{ opacity: 0, x: 30 }} whileInView={{ opacity: 1, x: 0 }} viewport={{ once: true }} className="glass-card rounded-2xl p-5 sm:p-6 md:p-10 border border-white/10 relative overflow-hidden min-h-[400px] flex items-center justify-center">
                <div className="absolute top-0 right-0 w-64 h-64 bg-blue-500/10 rounded-full mix-blend-screen filter blur-[60px]"></div>

                <div className="w-full relative z-10">
                  <AnimatePresence mode="wait">
                    <motion.div
                      key={activeFeatureTab}
                      initial={{ opacity: 0, y: 10, filter: "blur(4px)" }}
                      animate={{ opacity: 1, y: 0, filter: "blur(0px)" }}
                      exit={{ opacity: 0, y: -10, filter: "blur(4px)" }}
                      transition={{ duration: 0.3 }}
                    >
                      {FEATURES.find(f => f.id === activeFeatureTab)?.content}
                    </motion.div>
                  </AnimatePresence>
                </div>
              </motion.div>
            </div>
          </div>
        </div>
      </section>

      {/* --- How it Works Section --- */}
      <section id="how-it-works" className="py-16 lg:py-24 relative border-t border-white/5 bg-[#0B0D17]/50">
        <div className="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 relative z-10">
          <motion.div initial="hidden" whileInView="visible" viewport={{ once: true }} variants={fadeUpVar} className="text-center mb-10 sm:mb-16">
            <h2 className="text-3xl sm:text-4xl md:text-4xl font-bold mb-4">How it works</h2>
            <p className="text-base sm:text-lg text-slate-400 max-w-2xl mx-auto">Get your AI backoffice up and running in days, not months. No coding or complex setup required.</p>
          </motion.div>

          <motion.div
            initial="hidden" whileInView="visible" viewport={{ once: true, amount: 0.4 }} variants={staggerContainer}
            className="grid grid-cols-1 md:grid-cols-3 gap-8 sm:gap-10 relative mt-8 sm:mt-12"
          >
            {/* Animated Connecting Line */}
            <motion.div
              initial={{ scaleX: 0 }} whileInView={{ scaleX: 1 }} viewport={{ once: true }} transition={{ duration: 1.5, ease: "easeInOut" }}
              className="hidden md:block absolute top-12 left-[15%] right-[15%] h-0.5 bg-gradient-to-r from-blue-600/0 via-blue-500/50 to-blue-600/0 -z-10 origin-left"
            />

            {[
              { step: 1, title: 'Connect Your Systems', desc: 'Link Susana to your existing phone number, ServiceTitan, or Jobber account.', icon: Plug, color: 'blue' },
              { step: 2, title: 'Set Your Rules', desc: 'Upload your pricebook, service areas, and dispatch preferences.', icon: Settings2, color: 'purple' },
              { step: 3, title: 'Flip the Switch', desc: 'Turn Susana on. She immediately starts answering calls and booking jobs 24/7.', icon: Power, color: 'emerald' }
            ].map((item) => {
              const Icon = item.icon;
              return (
                <motion.div variants={popInVar} key={item.step} className="relative text-center group">
                  <motion.div whileHover={{ scale: 1.1, rotate: 5 }} className={`w-20 h-20 sm:w-24 sm:h-24 mx-auto glass-card rounded-full flex items-center justify-center mb-4 sm:mb-6 border border-white/10 group-hover:border-${item.color}-500/50 transition-colors z-10 relative bg-[#0B0D17]`}>
                    <div className={`absolute inset-0 bg-${item.color}-500/20 rounded-full blur-xl group-hover:bg-${item.color}-500/30 transition-colors`}></div>
                    <Icon className={`w-8 h-8 sm:w-10 sm:h-10 text-${item.color}-400 relative z-10`} />
                    <div className="absolute -top-1 -right-1 sm:-top-2 sm:-right-2 w-6 h-6 sm:w-8 sm:h-8 rounded-full bg-[#151828] border border-white/10 flex items-center justify-center font-bold text-xs sm:text-sm text-white">{item.step}</div>
                  </motion.div>
                  <h3 className="text-xl font-bold mb-2 sm:mb-3 text-white">{item.title}</h3>
                  <p className="text-slate-400 text-sm leading-relaxed px-2 sm:px-4">{item.desc}</p>
                </motion.div>
              )
            })}
          </motion.div>
        </div>
      </section>

      {/* --- Value Props Grid --- */}
      <section className="py-16 lg:py-24 bg-[#151828]/30 border-y border-white/5 relative overflow-hidden">
        <div className="absolute left-0 top-1/4 w-96 h-96 bg-purple-900/20 rounded-full blur-[100px] pointer-events-none"></div>
        <div className="absolute right-0 bottom-1/4 w-96 h-96 bg-blue-900/20 rounded-full blur-[100px] pointer-events-none"></div>

        <div className="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 relative z-10">
          <motion.div initial="hidden" whileInView="visible" viewport={{ once: true }} variants={fadeUpVar} className="mb-10 sm:mb-16">
            <h2 className="text-3xl sm:text-4xl font-bold mb-4">Built for the realities of the trade.</h2>
            <p className="text-base sm:text-lg text-slate-400 max-w-2xl">We understand that blue-collar businesses run differently than tech startups. Susana AI is hardwired for the field.</p>
          </motion.div>

          <motion.div initial="hidden" whileInView="visible" viewport={{ once: true, margin: "-50px" }} variants={staggerContainer} className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-5 sm:gap-6">
            {[
              { title: 'Never Miss a Lead', desc: "70% of customers go to the next company if you don't answer. Susana answers instantly.", icon: PhoneCall, color: 'blue' },
              { title: 'Intelligent Routing', desc: "Reads your dispatch board and assigns jobs to the closest tech with the right skills.", icon: MapPin, color: 'purple' },
              { title: 'Zero-Touch Invoicing', desc: "Generates an invoice from your pricebook, sends it, and follows up until paid.", icon: Receipt, color: 'emerald' },
              { title: 'Bilingual Support', desc: "Fluently speaks English and Spanish, automatically detecting the caller's language.", icon: Languages, color: 'amber' },
              { title: 'Omnichannel Intake', desc: "Unifies calls, texts, emails, or Google My Business messages into one thread.", icon: MessageSquare, color: 'rose' },
              { title: 'Spam & Tire-Kicker Filter', desc: "Blocks robocalls and qualifies leads, filtering out customers who aren't a fit.", icon: ShieldCheck, color: 'sky' }
            ].map((prop, idx) => {
              const Icon = prop.icon;
              return (
                <motion.div variants={fadeUpVar} whileHover={{ scale: 1.03, y: -5 }} key={idx} className={`glass-card p-6 sm:p-8 rounded-2xl border border-white/5 hover:border-${prop.color}-500/30 group cursor-default`}>
                  <div className={`w-12 h-12 rounded-xl bg-${prop.color}-500/20 text-${prop.color}-400 flex items-center justify-center mb-5 sm:mb-6`}>
                    <Icon className="w-6 h-6" />
                  </div>
                  <h3 className="text-xl font-bold mb-2 sm:mb-3 text-white">{prop.title}</h3>
                  <p className="text-slate-400 text-sm leading-relaxed">{prop.desc}</p>
                </motion.div>
              )
            })}
          </motion.div>
        </div>
      </section>

      {/* --- Integrations --- */}
      <section id="integrations" className="py-16 lg:py-24">
        <motion.div initial="hidden" whileInView="visible" viewport={{ once: true }} variants={staggerContainer} className="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 text-center">
          <motion.h2 variants={fadeUpVar} className="text-3xl font-bold mb-4 sm:mb-6">Works with the tools you already use</motion.h2>
          <motion.p variants={fadeUpVar} className="text-base sm:text-lg text-slate-400 mb-10 sm:mb-12 max-w-2xl mx-auto">No need to change your entire operation. Susana connects seamlessly with the leading field service management software.</motion.p>

          <motion.div variants={fadeUpVar} className="flex flex-wrap justify-center items-center gap-3 sm:gap-4">
            {['ServiceTitan', 'Housecall Pro', 'Jobber', 'QuickBooks'].map((tool, i) => (
              <div key={i} className="px-5 py-3 sm:px-6 sm:py-4 glass-card border border-white/10 rounded-xl flex items-center gap-3">
                <span className="font-semibold text-base sm:text-lg text-slate-200">{tool}</span>
              </div>
            ))}
          </motion.div>
        </motion.div>
      </section>

      {/* --- Pricing Section --- */}
      <section id="pricing" className="py-16 lg:py-24 relative border-t border-white/5 bg-[#0B0D17]/50 overflow-hidden">
        <motion.div animate={{ scale: [1, 1.2, 1], opacity: [0.1, 0.2, 0.1] }} transition={{ duration: 10, repeat: Infinity, ease: "easeInOut" }} className="absolute top-0 right-0 w-[600px] h-[600px] bg-blue-600 rounded-full blur-[120px] pointer-events-none -z-10"></motion.div>
        <motion.div animate={{ scale: [1, 1.2, 1], opacity: [0.1, 0.2, 0.1] }} transition={{ duration: 10, repeat: Infinity, ease: "easeInOut", delay: 5 }} className="absolute bottom-0 left-0 w-[600px] h-[600px] bg-purple-600 rounded-full blur-[120px] pointer-events-none -z-10"></motion.div>

        <div className="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 relative z-10">
          <motion.div initial="hidden" whileInView="visible" viewport={{ once: true }} variants={fadeUpVar} className="text-center mb-10 sm:mb-16">
            <h2 className="text-3xl sm:text-4xl md:text-5xl font-bold mb-4">The Complete Virtual Backoffice</h2>
            <p className="text-base sm:text-lg text-slate-400 max-w-2xl mx-auto">Powered by our proprietary AI routing engine. Turn missed calls into booked jobs, automatically chase invoices, and capture 5-star reviews.</p>
          </motion.div>

          <motion.div initial="hidden" whileInView="visible" viewport={{ once: true, margin: "-50px" }} variants={staggerContainer} className="grid grid-cols-1 md:grid-cols-3 gap-6 lg:gap-8 max-w-6xl mx-auto items-center">

            {/* Starter Tier */}
            <motion.div variants={fadeUpVar} className="glass-card p-8 rounded-2xl border border-white/10 relative flex flex-col h-full">
              <h3 className="text-xl font-bold text-white mb-2">Call Catcher</h3>
              <p className="text-sm text-slate-400 mb-6">Stop bleeding leads when you're under a sink or on a roof.</p>
              <div className="mb-6">
                <span className="text-4xl font-extrabold text-white">$199</span>
                <span className="text-slate-400 font-medium">/mo</span>
              </div>
              <ul className="space-y-4 mb-8 text-sm text-slate-300 flex-1">
                <li className="flex items-start gap-3"><Check className="w-5 h-5 text-blue-400 flex-shrink-0" /> <span><strong>3 Off-the-Shelf Workflows:</strong> Ready to use on day one.</span></li>
                <li className="flex items-start gap-3"><Check className="w-5 h-5 text-blue-400 flex-shrink-0" /> <span><strong>Emergency Missed Call Catcher:</strong> Instant SMS intercept.</span></li>
                <li className="flex items-start gap-3"><Check className="w-5 h-5 text-blue-400 flex-shrink-0" /> <span>Smart Intent Classification & MMS Processing</span></li>
                <li className="flex items-start gap-3"><Check className="w-5 h-5 text-blue-400 flex-shrink-0" /> <span>Direct SMS Handoff to your cell</span></li>
              </ul>
              <motion.button
                whileHover={{ scale: 1.02 }} whileTap={{ scale: 0.98 }}
                onClick={() => handleStripeCheckout('Call Catcher', 199, false)}
                disabled={checkoutLoading === 'Call Catcher'}
                className="block w-full py-3 px-4 bg-white/5 hover:bg-white/10 text-white text-center font-semibold rounded-lg border border-white/10 transition-colors"
              >
                {checkoutLoading === 'Call Catcher' ? <Loader2 className="w-5 h-5 animate-spin mx-auto" /> : 'Start Basic'}
              </motion.button>
            </motion.div>

            {/* Pro Tier */}
            <motion.div variants={fadeUpVar} className="glass-card p-8 rounded-2xl border border-blue-500 relative transform md:-translate-y-4 shadow-[0_0_40px_rgba(37,99,235,0.2)] bg-[#0B0D17] flex flex-col h-full z-10">
              <div className="absolute top-0 left-1/2 -translate-x-1/2 -translate-y-1/2 bg-gradient-to-r from-blue-600 to-purple-600 text-white text-xs font-bold px-4 py-1.5 rounded-full uppercase tracking-wide whitespace-nowrap shadow-lg">
                The Full Backoffice
              </div>
              <h3 className="text-xl font-bold text-white mb-2 mt-2">Autonomous Pro</h3>
              <p className="text-sm text-blue-200 mb-6 font-medium bg-blue-500/10 p-2 rounded border border-blue-500/20 inline-block">Requires $1,000 White-Glove Setup Fee</p>
              <div className="mb-6">
                <span className="text-4xl font-extrabold text-white">$499</span>
                <span className="text-slate-400 font-medium">/mo</span>
              </div>
              <ul className="space-y-4 mb-8 text-sm text-slate-300 flex-1">
                <li className="flex items-start gap-3"><Zap className="w-5 h-5 text-amber-400 flex-shrink-0" /> <span><strong>Custom Workflows:</strong> Tailored AI flows for your rules.</span></li>
                <li className="flex items-start gap-3"><Zap className="w-5 h-5 text-amber-400 flex-shrink-0" /> <span><strong>White-Glove Telecom:</strong> Map Conditional Call Forwarding.</span></li>
                <li className="flex items-start gap-3"><Check className="w-5 h-5 text-blue-400 flex-shrink-0" /> <span><strong>Tire-Kicker Qualifier:</strong> Estimate calculation via pricebook.</span></li>
                <li className="flex items-start gap-3"><Check className="w-5 h-5 text-blue-400 flex-shrink-0" /> <span><strong>On-My-Way & Review Trap:</strong> Alerts and follow-ups.</span></li>
                <li className="flex items-start gap-3"><Check className="w-5 h-5 text-blue-400 flex-shrink-0" /> <span><strong>Invoice Chaser:</strong> Automated payment reminders.</span></li>
              </ul>
              <motion.button
                whileHover={{ scale: 1.02 }} whileTap={{ scale: 0.98 }}
                onClick={() => handleStripeCheckout('Autonomous Pro', 499, true)}
                disabled={checkoutLoading === 'Autonomous Pro'}
                className="block w-full py-3 px-4 bg-blue-600 hover:bg-blue-500 text-white text-center font-bold rounded-lg shadow-lg"
              >
                {checkoutLoading === 'Autonomous Pro' ? <Loader2 className="w-5 h-5 animate-spin mx-auto" /> : 'Apply for Pro Setup'}
              </motion.button>
            </motion.div>

            {/* Scale Tier */}
            <motion.div variants={fadeUpVar} className="glass-card p-8 rounded-2xl border border-white/10 relative flex flex-col h-full">
              <h3 className="text-xl font-bold text-white mb-2">Fleet Enterprise</h3>
              <p className="text-sm text-slate-400 mb-6">For multi-truck operations with complex dispatching rules.</p>
              <div className="mb-6">
                <span className="text-4xl font-extrabold text-white">Custom</span>
              </div>
              <ul className="space-y-4 mb-8 text-sm text-slate-300 flex-1">
                <li className="flex items-start gap-3"><Check className="w-5 h-5 text-blue-400 flex-shrink-0" /> <span>Everything in Autonomous Pro</span></li>
                <li className="flex items-start gap-3"><Users className="w-5 h-5 text-blue-400 flex-shrink-0" /> <span><strong>Human-in-the-Loop:</strong> Routing to live agents for edge cases.</span></li>
                <li className="flex items-start gap-3"><Check className="w-5 h-5 text-blue-400 flex-shrink-0" /> <span>Multi-Truck & Territory routing</span></li>
                <li className="flex items-start gap-3"><Check className="w-5 h-5 text-blue-400 flex-shrink-0" /> <span>Direct API read/write to ServiceTitan</span></li>
                <li className="flex items-start gap-3"><Check className="w-5 h-5 text-blue-400 flex-shrink-0" /> <span>Dedicated Account Manager</span></li>
              </ul>
              <motion.a whileHover={{ scale: 1.02 }} whileTap={{ scale: 0.98 }} href="#contact" className="block w-full py-3 px-4 bg-white/5 hover:bg-white/10 text-white text-center font-semibold rounded-lg border border-white/10 transition-colors">
                Talk to Sales
              </motion.a>
            </motion.div>
          </motion.div>
        </div>
      </section>

      {/* --- Contact Form --- */}
      <section id="contact" className="py-16 lg:py-24 relative overflow-hidden bg-[#151828]/80 border-t border-white/5">
        <div className="absolute top-1/2 left-1/2 -translate-x-1/2 -translate-y-1/2 w-[800px] h-[800px] bg-blue-600/10 rounded-full blur-[120px] pointer-events-none"></div>

        <motion.div initial="hidden" whileInView="visible" viewport={{ once: true }} variants={fadeUpVar} className="max-w-4xl mx-auto px-4 sm:px-6 lg:px-8 relative z-10">
          <div className="text-center mb-10 sm:mb-12">
            <h2 className="text-3xl sm:text-4xl font-bold mb-4">Ready to hire Susana?</h2>
            <p className="text-base sm:text-lg text-slate-400">Fill out the form below to see a custom demo of Susana AI operating specifically for your trade business.</p>
          </div>

          <div className="glass-card p-6 sm:p-8 md:p-10 rounded-2xl border border-white/10">
            <form onSubmit={handleContactSubmit} className="space-y-5 sm:space-y-6">
              <div className="grid grid-cols-1 md:grid-cols-2 gap-5 sm:gap-6">
                <div>
                  <label className="block text-sm font-medium text-slate-300 mb-2">First Name</label>
                  <input type="text" name="firstName" required className="w-full bg-[#0B0D17]/50 border border-white/10 rounded-lg px-4 py-3 text-white placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition-all" />
                </div>
                <div>
                  <label className="block text-sm font-medium text-slate-300 mb-2">Last Name</label>
                  <input type="text" name="lastName" required className="w-full bg-[#0B0D17]/50 border border-white/10 rounded-lg px-4 py-3 text-white placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition-all" />
                </div>
              </div>
              <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
                <div>
                  <label className="block text-sm font-medium text-slate-300 mb-2">Work Email</label>
                  <input type="email" name="email" required placeholder="you@company.com" className="w-full bg-[#0B0D17]/50 border border-white/10 rounded-lg px-4 py-3 text-white placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition-all" />
                </div>
                <div>
                  <label className="block text-sm font-medium text-slate-300 mb-2">Phone Number</label>
                  <input type="tel" name="phone" required placeholder="(555) 000-0000" className="w-full bg-[#0B0D17]/50 border border-white/10 rounded-lg px-4 py-3 text-white placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition-all" />
                </div>
              </div>
              <div className="grid grid-cols-1 md:grid-cols-2 gap-6">
                <div>
                  <label className="block text-sm font-medium text-slate-300 mb-2">Company Name</label>
                  <input type="text" name="company" required className="w-full bg-[#0B0D17]/50 border border-white/10 rounded-lg px-4 py-3 text-white placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition-all" />
                </div>
                <div>
                  <label className="block text-sm font-medium text-slate-300 mb-2">Industry</label>
                  <select name="industry" required defaultValue="" className="w-full bg-[#0B0D17]/50 border border-white/10 rounded-lg px-4 py-3 text-white focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition-all appearance-none">
                    <option value="" disabled>Select your trade...</option>
                    <option value="hvac">HVAC</option>
                    <option value="plumbing">Plumbing</option>
                    <option value="electrical">Electrical</option>
                    <option value="roofing">Roofing</option>
                  </select>
                </div>
              </div>
              <div>
                <label className="block text-sm font-medium text-slate-300 mb-2">What is your biggest backoffice pain point?</label>
                <textarea name="message" rows="4" placeholder="E.g., I miss too many calls while on the job..." className="w-full bg-[#0B0D17]/50 border border-white/10 rounded-lg px-4 py-3 text-white placeholder-slate-500 focus:outline-none focus:ring-2 focus:ring-blue-500 focus:border-transparent transition-all resize-none"></textarea>
              </div>

              <motion.button whileHover={{ scale: 1.01 }} whileTap={{ scale: 0.99 }} type="submit" disabled={isFormProcessing} className="w-full bg-blue-600 hover:bg-blue-500 text-white font-bold py-3.5 sm:py-4 rounded-lg shadow-[0_0_20px_rgba(37,99,235,0.4)] flex justify-center items-center gap-2">
                {isFormProcessing ? <><Loader2 className="w-5 h-5 animate-spin" /> Processing...</> : <>Get Custom Demo <Send className="w-5 h-5" /></>}
              </motion.button>

              <AnimatePresence>
                {isFormSuccess && (
                  <motion.div initial={{ opacity: 0, height: 0 }} animate={{ opacity: 1, height: 'auto' }} exit={{ opacity: 0, height: 0 }} className="text-center text-emerald-400 font-medium p-4 bg-emerald-500/10 border border-emerald-500/20 rounded-lg">
                    Thanks! We've received your information and will be in touch shortly.
                  </motion.div>
                )}
              </AnimatePresence>
            </form>
          </div>
        </motion.div>
      </section>

      {/* --- Footer --- */}
      <footer className="bg-[#0B0D17] border-t border-white/5 py-10 sm:py-12">
        <div className="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 flex flex-col md:flex-row justify-between items-center gap-6">
          <a href="#" className="flex items-center">
            <div className="px-3 py-1 rounded-lg bg-gradient-to-r from-blue-600 to-purple-600 shadow-md shadow-blue-500/20 border border-white/10">
              <span className="font-bold text-lg text-white">Susana AI</span>
            </div>
          </a>
          <div className="text-slate-500 text-sm text-center md:text-left">
            &copy; {new Date().getFullYear()} susanaai.com. All rights reserved.
          </div>
          <div className="flex flex-wrap justify-center gap-4 sm:gap-6">
            <a href="#" className="text-slate-500 hover:text-white transition-colors text-sm sm:text-base">Privacy</a>
            <a href="#" className="text-slate-500 hover:text-white transition-colors text-sm sm:text-base">Terms</a>
            <a href="#" className="text-slate-500 hover:text-white transition-colors text-sm sm:text-base">Contact</a>
          </div>
        </div>
      </footer>

      {/* --- Stripe Checkout Redirect Loading Overlay --- */}
      <AnimatePresence>
        {checkoutLoading && (
          <motion.div
            initial={{ opacity: 0 }} animate={{ opacity: 1 }} exit={{ opacity: 0 }}
            className="fixed inset-0 z-[100] flex flex-col items-center justify-center bg-[#0B0D17]/95 backdrop-blur-md px-4 text-center"
          >
            <Loader2 className="w-12 h-12 text-blue-500 animate-spin mb-6" />
            <h3 className="text-2xl font-bold text-white mb-2">Connecting securely to Stripe...</h3>
            <p className="text-slate-400">Preparing your <strong>{checkoutLoading}</strong> checkout session.</p>
          </motion.div>
        )}
      </AnimatePresence>

    </div>
  );
}