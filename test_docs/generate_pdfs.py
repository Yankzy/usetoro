from fpdf import FPDF
import os

def create_bank_statement(filename):
    pdf = FPDF()
    pdf.add_page()
    
    # Header
    pdf.set_font("helvetica", "B", 16)
    pdf.cell(0, 10, "ATTIJARIWAFA BANK - Relevé de Compte", new_x="LMARGIN", new_y="NEXT", align='L')
    
    pdf.set_font("helvetica", "", 11)
    pdf.cell(0, 7, "Client: Atlas Office Solutions SARL", new_x="LMARGIN", new_y="NEXT")
    pdf.cell(0, 7, "ICE Client: 001234567000089", new_x="LMARGIN", new_y="NEXT")
    pdf.cell(0, 7, "Compte N°: 007810000012345678901234", new_x="LMARGIN", new_y="NEXT")
    pdf.cell(0, 7, "Période: 01/07/2026 au 31/07/2026", new_x="LMARGIN", new_y="NEXT")
    pdf.ln(6)
    
    # Table Header
    pdf.set_font("helvetica", "B", 9)
    col_widths = [22, 85, 24, 24, 28]
    headers = ['Date', 'Libellé', 'Débit (MAD)', 'Crédit (MAD)', 'Solde (MAD)']
    
    for i, header in enumerate(headers):
        pdf.cell(col_widths[i], 8, header, border=1, align='C')
    pdf.ln(8)
    
    # Table Data
    pdf.set_font("helvetica", "", 8)
    data = [
        ['01/07/2026', 'Solde Précédent', '', '', '150,000.00'],
        ['02/07/2026', 'Loyer Juil. - Soc. Immobiliere Anfa', '12,000.00', '', '138,000.00'],
        ['05/07/2026', 'Virement Client ABC Construction SARL', '', '25,000.00', '163,000.00'],
        ['08/07/2026', 'Paiement CB Station Afriquia Oasis', '850.00', '', '162,150.00'],
        ['10/07/2026', 'Achats Fournitures Marjane Business HQ', '2,400.00', '', '159,750.00'],
        ['12/07/2026', 'Virement Client XYZ Industrie SA', '', '42,000.00', '201,750.00'],
        ['15/07/2026', 'Prelevement Facture Orange Maroc SA', '1,200.00', '', '200,550.00'],
        ['18/07/2026', 'Prelevement Mensuel CNSS', '6,800.00', '', '193,750.00'],
        ['20/07/2026', 'Prelevement Facture Lydec Casablanca', '1,450.00', '', '192,300.00'],
        ['22/07/2026', 'Honoraires Cabinet Comptable El Fassi', '3,500.00', '', '188,800.00'],
        ['25/07/2026', 'Virement Recu Technopark IT Solutions', '', '18,500.00', '207,300.00'],
        ['27/07/2026', 'Prelevement Maroc Telecom (IAM)', '950.00', '', '206,350.00'],
        ['28/07/2026', 'Paiement Imprimerie Moderne SARL', '1,800.00', '', '204,550.00'],
        ['30/07/2026', 'Frais Tenue de Compte Attijariwafa Bank', '165.00', '', '204,385.00'],
        ['31/07/2026', 'Solde Fin de Mois', '', '', '204,385.00'],
    ]
    
    for row in data:
        for i, item in enumerate(row):
            align = 'L' if i == 1 else 'R' if i > 1 else 'C'
            pdf.cell(col_widths[i], 7, item, border=1, align=align)
        pdf.ln(7)
        
    pdf.output(filename)
    print(f"Created {filename}")


def create_facture(filename):
    pdf = FPDF()
    pdf.add_page()
    
    # Header
    pdf.set_font("helvetica", "B", 18)
    pdf.cell(0, 10, "FACTURE N° F2026-0712", new_x="LMARGIN", new_y="NEXT", align='C')
    pdf.ln(10)
    
    pdf.set_font("helvetica", "B", 14)
    pdf.cell(0, 8, "Orange Maroc SA", new_x="LMARGIN", new_y="NEXT")
    pdf.set_font("helvetica", "", 12)
    pdf.cell(0, 6, "Adresse: Boulevard Moulay Ismail, Casablanca, Maroc", new_x="LMARGIN", new_y="NEXT")
    pdf.cell(0, 6, "ICE: 001524311000045", new_x="LMARGIN", new_y="NEXT")
    pdf.ln(10)
    
    pdf.set_font("helvetica", "B", 12)
    pdf.cell(0, 8, "Client: Atlas Office Solutions SARL", new_x="LMARGIN", new_y="NEXT")
    pdf.set_font("helvetica", "", 12)
    pdf.cell(0, 6, "Adresse: Boulevard Anfa, Casablanca, Maroc", new_x="LMARGIN", new_y="NEXT")
    pdf.cell(0, 6, "ICE Client: 001234567000089", new_x="LMARGIN", new_y="NEXT")
    pdf.cell(0, 6, "Date: 15/07/2026", new_x="LMARGIN", new_y="NEXT")
    pdf.ln(15)
    
    # Table Header
    pdf.set_font("helvetica", "B", 11)
    col_widths = [100, 20, 35, 35]
    headers = ['Désignation', 'Qté', 'PU (MAD)', 'HT (MAD)']
    
    for i, header in enumerate(headers):
        pdf.cell(col_widths[i], 10, header, border=1, align='C')
    pdf.ln(10)
    
    # Table Data
    pdf.set_font("helvetica", "", 10)
    data = [
        ['Abonnement Télécom & Fibre Optique Business', '1', '1,000.00', '1,000.00'],
    ]
    
    for row in data:
        for i, item in enumerate(row):
            align = 'L' if i == 0 else 'R' if i > 0 else 'C'
            pdf.cell(col_widths[i], 8, item, border=1, align=align)
        pdf.ln(8)
        
    pdf.ln(10)
    
    # Totals
    pdf.set_font("helvetica", "", 11)
    pdf.cell(120, 8, "", border=0)
    pdf.cell(35, 8, "Total HT", border=1, align='R')
    pdf.cell(35, 8, "1,000.00", border=1, align='R')
    pdf.ln(8)
    
    pdf.cell(120, 8, "", border=0)
    pdf.cell(35, 8, "TVA (20%)", border=1, align='R')
    pdf.cell(35, 8, "200.00", border=1, align='R')
    pdf.ln(8)
    
    pdf.set_font("helvetica", "B", 11)
    pdf.cell(120, 8, "", border=0)
    pdf.cell(35, 8, "Total TTC", border=1, align='R')
    pdf.cell(35, 8, "1,200.00", border=1, align='R')
    pdf.ln(8)
    
    pdf.output(filename)
    print(f"Created {filename}")

if __name__ == "__main__":
    dir_path = os.path.dirname(os.path.realpath(__file__))
    create_bank_statement(os.path.join(dir_path, "releve_bancaire.pdf"))
    create_facture(os.path.join(dir_path, "facture.pdf"))
