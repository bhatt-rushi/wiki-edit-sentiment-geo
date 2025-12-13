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
      app init-db
      app fetch-articles
      app revision-fetch
      app compare-labels
    """
    if len(sys.argv) > 1:
        cmd = sys.argv[1]
        debug_mode = "--debug" in sys.argv
        
        if cmd == "init-db":
            init_db()
            return
        elif cmd == "fetch-articles":
            url = None
            if len(sys.argv) > 2:
                url = sys.argv[2]
            get_articles(specific_url=url)
            return
        elif cmd == "revision-fetch":
            # Ensure we have articles to process
            if not os.path.exists(DB_PATH):
                init_db()
            
            conn = sqlite3.connect(DB_PATH)
            c = conn.cursor()
            try:
                c.execute("SELECT COUNT(*) FROM articles")
                if c.fetchone()[0] == 0:
                    print("No articles found in DB. Fetching articles...")
                    get_articles()
            except sqlite3.OperationalError:
                init_db()
                get_articles()
            conn.close()

            limit = None
            workers_per_gpu = 1
            if "--limit" in sys.argv:
                try:
                    limit_idx = sys.argv.index("--limit")
                    if limit_idx + 1 < len(sys.argv):
                        limit = int(sys.argv[limit_idx + 1])
                except ValueError:
                    print("Invalid limit value provided.")
            
            if "--workers-per-gpu" in sys.argv:
                try:
                    w_idx = sys.argv.index("--workers-per-gpu")
                    if w_idx + 1 < len(sys.argv):
                        workers_per_gpu = int(sys.argv[w_idx + 1])
                except ValueError:
                    print("Invalid workers-per-gpu value provided.")
            
            get_revisions(debug_mode=debug_mode, limit=limit, workers_per_gpu=workers_per_gpu)
            return
        elif cmd == "compare-labels":
            article_url = None
            if len(sys.argv) > 2:
                article_url = sys.argv[2]
            compare_labels(article_url)
            return
        else:
            print(f"Unknown command: {cmd}")
            print("Usage: app [init-db | fetch-articles [url] | revision-fetch [--debug] [--limit N] [--workers-per-gpu N] | compare-labels [article_url]]")
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
