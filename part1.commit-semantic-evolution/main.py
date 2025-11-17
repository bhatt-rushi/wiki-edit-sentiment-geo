import os
from scripts.get_articles import get_articles
from scripts.get_revisions import get_revisions

def main():
    """
    Main function to orchestrate the data fetching process.
    """
    articles_file = "data/articles.json"
    revisions_file = "data/revisions.json"

    if not os.path.exists(articles_file) or os.path.getsize(articles_file) == 0:
        print("articles.json not found or is empty. Fetching articles...")
        get_articles()
        print("Finished fetching articles.")
    else:
        print("articles.json already exists and is not empty. Skipping.")

    if not os.path.exists(revisions_file) or os.path.getsize(revisions_file) == 0:
        print("revisions.json not found or is empty. Fetching revisions...")
        get_revisions()
        print("Finished fetching revisions.")
    else:
        print("revisions.json already exists and is not empty. Skipping.")

if __name__ == "__main__":
    main()
