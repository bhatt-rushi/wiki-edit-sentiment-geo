import os
import sys
import sqlite3
from scripts.get_articles import get_articles
from scripts.get_revisions import get_revisions
from scripts.init_db import init_db
from scripts.compare_labels import main as compare_labels

DB_PATH = "data/wiki.db"

def main():
    """
    Main function to orchestrate the data fetching process.
    Supports CLI commands:
      app revision-fetch-translated
      app fetch-articles
      app init-db
      app compare-labels
    """
    if len(sys.argv) > 1:
        cmd = sys.argv[1]
        debug_mode = "--debug" in sys.argv
        
        if cmd == "revision-fetch-translated":
            get_revisions(debug_mode=debug_mode)
            return
        elif cmd == "fetch-articles":
            url = None
            if len(sys.argv) > 2:
                url = sys.argv[2]
            get_articles(specific_url=url)
            return
        elif cmd == "init-db":
            init_db()
            return
        elif cmd == "compare-labels":
            article_url = None
            if len(sys.argv) > 2:
                article_url = sys.argv[2]
            compare_labels(article_url)
            return
        else:
            print(f"Unknown command: {cmd}")
            print("Usage: app [revision-fetch-translated [--debug] | fetch-articles [url] | init-db | compare-labels [article_url]]")
            return

    # Default behavior if no arguments provided (check DB and run if empty)
    if not os.path.exists(DB_PATH) or os.path.getsize(DB_PATH) == 0:
        print("Database not found or is empty. Initializing DB...")
        init_db()
        print("Finished initializing DB.")

    conn = sqlite3.connect(DB_PATH)
    c = conn.cursor()
    
    # Check if articles exist
    c.execute("SELECT COUNT(*) FROM articles")
    article_count = c.fetchone()[0]
    if article_count == 0:
        print("No articles found in DB. Fetching articles...")
        get_articles()
        print("Finished fetching articles.")
    else:
        print(f"{article_count} articles already exist. Skipping article fetch.")

    # Check if revisions exist
    c.execute("SELECT COUNT(*) FROM revisions")
    revision_count = c.fetchone()[0]
    if revision_count == 0:
        print("No revisions found in DB. Fetching revisions...")
        get_revisions()
        print("Finished fetching revisions.")
    else:
        print(f"{revision_count} revisions already exist. Skipping revision fetch.")
    
    conn.close()

if __name__ == "__main__":
    main()
