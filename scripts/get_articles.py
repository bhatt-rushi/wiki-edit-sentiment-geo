import json
import urllib.parse
import urllib.request
import random
import sqlite3

DB_PATH = "data/wiki.db"

def get_random_coord():
    # Hsinchu region approx bounds
    # Lat: 24.5 - 25.0
    # Lon: 120.9 - 121.4
    lat = random.uniform(24.5, 25.0)
    lon = random.uniform(120.9, 121.4)
    return f"POINT({lon:.5f} {lat:.5f})"

def parse_wkt(wkt_str):
    if not wkt_str: return None
    return wkt_str.upper()

def get_articles():
    conn = sqlite3.connect(DB_PATH)
    c = conn.cursor()

    # Clear existing articles to avoid duplicates if re-run without full DB reset
    c.execute("DELETE FROM articles")
    
    # (Rest of the function remains similar, but inserts into DB instead of JSON)
    # ...

    with open("queries/query.sparql", "r") as f:
        query = f.read()

    endpoint = "https://query.wikidata.org/sparql"
    params = {"query": query, "format": "json"}
    url = endpoint + "?" + urllib.parse.urlencode(params)

    req = urllib.request.Request(url, headers={"User-Agent": "MyResearchBot/1.0"})
    
    with urllib.request.urlopen(req) as response:
        data = json.loads(response.read().decode())

    existing_coords = set()

    for item in data["results"]["bindings"]:
        url = item["article"]["value"]
        coord_raw = item.get("coord", {}).get("value")
        
        if coord_raw:
            clean_coord = parse_wkt(coord_raw)
        else:
            clean_coord = get_random_coord()
            while clean_coord in existing_coords:
                 clean_coord = get_random_coord()
        
        existing_coords.add(clean_coord)
        
        c.execute("INSERT OR REPLACE INTO articles (url, coord) VALUES (?, ?)", (url, clean_coord))

    conn.commit()
    conn.close()
    print(f"Inserted {len(data['results']['bindings'])} articles into {DB_PATH}")
